# S3 release mirror (one-time AWS setup)

Servers install hostlink from an S3 mirror instead of GitHub, so a first boot
never depends on GitHub's unauthenticated API (60 requests/hour per IP; AWS
reuses public IPs) or on GitHub being reachable. Every tagged release is copied
there by `.github/workflows/release.yml` → `scripts/release/mirror_to_s3.sh`:

```
s3://<bucket>/hostlink/<tag>/hostlink_Linux_x86_64.tar.gz
s3://<bucket>/hostlink/<tag>/hostlink_Linux_arm64.tar.gz
s3://<bucket>/hostlink/<tag>/hostlink_<version>_checksums.txt
s3://<bucket>/hostlink/<tag>/install.sh
```

A server then runs:

```sh
curl -fsSL https://<bucket>.s3.<region>.amazonaws.com/hostlink/<tag>/install.sh -o install.sh
sudo bash install.sh --server-url … --token-id … --token-key … \
  --version <tag> --download-base-url https://<bucket>.s3.<region>.amazonaws.com/hostlink/<tag> \
  --sha256-x86_64 <sha> --sha256-arm64 <sha>
```

The mirror step is skipped until the setup below is done, so releases are
unaffected meanwhile.

## Values

| Name | Value |
|---|---|
| AWS account | SelfHost production |
| Region | `ap-south-1` |
| Bucket | `selfhost-hostlink-releases` |
| IAM role | `hostlink-release-mirror` |

## 1. Bucket

The objects are the same open-source binaries already public on GitHub
releases, so the `hostlink/` prefix is public-read. Nothing else in the bucket is.

```sh
aws s3api create-bucket --bucket selfhost-hostlink-releases --region ap-south-1 \
  --create-bucket-configuration LocationConstraint=ap-south-1
# Versioning: an accidental overwrite of a release object stays recoverable.
aws s3api put-bucket-versioning --bucket selfhost-hostlink-releases \
  --versioning-configuration Status=Enabled
# Allow a bucket policy to grant public read (ACLs stay blocked).
aws s3api put-public-access-block --bucket selfhost-hostlink-releases \
  --public-access-block-configuration BlockPublicAcls=true,IgnorePublicAcls=true,BlockPublicPolicy=false,RestrictPublicBuckets=false
aws s3api put-bucket-policy --bucket selfhost-hostlink-releases --policy '{
  "Version": "2012-10-17",
  "Statement": [{
    "Sid": "PublicReadHostlinkReleases",
    "Effect": "Allow",
    "Principal": "*",
    "Action": "s3:GetObject",
    "Resource": "arn:aws:s3:::selfhost-hostlink-releases/hostlink/*"
  }]
}'
```

## 2. IAM role for the release workflow (GitHub OIDC)

The account already trusts GitHub's OIDC provider
(`token.actions.githubusercontent.com`) for the SelfHost deploy. Check with
`aws iam list-open-id-connect-providers`; create it only if missing.

The release job declares no `environment:`, so its token's subject is the tag
ref: `repo:selfhost-dev/hostlink:ref:refs/tags/<tag>`. Trust only tags of this
repository:

```sh
ACCOUNT_ID=$(aws sts get-caller-identity --query Account --output text)
aws iam create-role --role-name hostlink-release-mirror --assume-role-policy-document "{
  \"Version\": \"2012-10-17\",
  \"Statement\": [{
    \"Effect\": \"Allow\",
    \"Principal\": { \"Federated\": \"arn:aws:iam::${ACCOUNT_ID}:oidc-provider/token.actions.githubusercontent.com\" },
    \"Action\": \"sts:AssumeRoleWithWebIdentity\",
    \"Condition\": {
      \"StringEquals\": { \"token.actions.githubusercontent.com:aud\": \"sts.amazonaws.com\" },
      \"StringLike\": { \"token.actions.githubusercontent.com:sub\": \"repo:selfhost-dev/hostlink:ref:refs/tags/*\" }
    }
  }]
}"
aws iam put-role-policy --role-name hostlink-release-mirror --policy-name mirror-upload --policy-document '{
  "Version": "2012-10-17",
  "Statement": [{
    "Effect": "Allow",
    "Action": "s3:PutObject",
    "Resource": "arn:aws:s3:::selfhost-hostlink-releases/hostlink/*"
  }]
}'
```

If a run fails to assume the role, read the token's actual `sub` from the
failed step (GitHub also mints an `@id` form, e.g.
`repo:selfhost-dev@<owner-id>/hostlink@<repo-id>:…`) and add it to the
`StringLike` list rather than widening the pattern.

## 3. Repository variables (hostlink → Settings → Variables → Actions)

| Variable | Value |
|---|---|
| `HOSTLINK_MIRROR_BUCKET` | `selfhost-hostlink-releases` |
| `HOSTLINK_MIRROR_ROLE_ARN` | `arn:aws:iam::<account-id>:role/hostlink-release-mirror` |
| `HOSTLINK_MIRROR_REGION` | `ap-south-1` |

## 4. Backfill the current release

Releases tagged before the variables existed are not mirrored. Copy the one
SelfHost pins (from a checkout of this repo, with the same AWS account):

```sh
TAG=v0.8.45; mkdir -p /tmp/hl-dist && cd /tmp/hl-dist
gh release download "$TAG" --repo selfhost-dev/hostlink \
  --pattern 'hostlink_Linux_*.tar.gz' --pattern 'hostlink_*_checksums.txt'
cd - && scripts/release/mirror_to_s3.sh "$TAG" /tmp/hl-dist s3://selfhost-hostlink-releases/hostlink
```

`install.sh` is copied from the checkout, so backfill from a checkout that has
`--version` / `--download-base-url` (this change or later).

## 5. Verify

```sh
curl -sI https://selfhost-hostlink-releases.s3.ap-south-1.amazonaws.com/hostlink/v0.8.45/hostlink_Linux_arm64.tar.gz | head -1   # HTTP/1.1 200
curl -sI https://selfhost-hostlink-releases.s3.ap-south-1.amazonaws.com/other-key | head -1                                      # 403: only hostlink/ is public
```

Then tag the next release and check the workflow's "Mirror release to S3" step
ran and printed `Mirrored hostlink <tag> to s3://selfhost-hostlink-releases/hostlink/<tag>/`.
