package crypto

import (
	"crypto/x509"
	"encoding/pem"
	"testing"
)

// An envelope sealed by the control plane's Tasks::SealedEnv (Ruby OpenSSL), with the key it was
// sealed to: the two implementations must agree on every byte of the format (selfhost #3168).
const railsVectorKey = `-----BEGIN RSA PRIVATE KEY-----
MIIEowIBAAKCAQEAtt/0dU3XqjIU1Kq++0XYovMXy8AGaOMsRAB4SjY99ZnEgWvK
XyeDabZy58RGqwtgW2JSdVoEA/LRdhFA/B2sKRHAk7txyRYLMEQvebsVxKyd2mo/
UmX32rqVNvpun8r4f0xKfHqrJsRrD8h0kkWfxjdBhjB1dTDG5uKkLiFUVtmJNZYW
iUrUK/6uE61cSHgXUoBcr74ejfcePeK0ALOCwTX6LaxWb4O4Irox9UtmDjwQw4em
rUJmndBga1IPFCAyhke6FF1Syj5SVL0Nhij5TivC91TxteiJYxF3r4TnmkbjTgat
LSg3CjoOx4nvbK1Sj6GezR3+ZgTOU5iZexCIoQIDAQABAoIBAAQkA897vaEOm3BW
e2OKHDketioe+D/Iup7BjSroM05YnUPtUmEFyB2mvwKza+kBWKNJEYmAFvlitps5
q2Lp/+HTYCzLWjmA3iwvs9l9D6WeRcBSMCPbLjNnW4VdD0Mr+XHWCUiFqIY347d9
3otvUYpPg8eYWLIYf1bVlX0VgRwREz/wTEGBvZ7gt+BNwi9UlZCipI54vx77NESW
wOQSOoWSXWT13Kje1ktQRBPIMxMfoc5fLhpdPRHF1kLYGo1jZGG1n8Bzx4wFS+P1
3C3Em9NVhywwzO02wz5J7WMU8JgPIVxJdQNYe+rXoQ45mp28PmGmwlgbAmARHIwk
IzS2CEkCgYEA6bLHcaUzsxKS9MDahrHUwEaql1b49RSnpC10FHJfVeZXTxDrdwdf
3G30Iun+jAvw7/5RxYrtXjRyEYPpvsM+4Dzhy0wEiyJfW2iHY45F9hcTqmE6jRSe
/TrPHUCjlXIqu3/hxHhHQ1JfDjpO2Ywcj7BAWfMPMDOms+8FplkuDW0CgYEAyFOQ
PsJhf0qHsnQYq/YvHX2gdtBb/v67WBRK95rm66kDqYVTBtstBmm3wjI5bvl+bgpo
8v/WrLLpcDzcpLsF5ogOTNOShZ2dqhcocTjgaJ03PAS1kgRiOZktcr1I/cueYt9g
L7KIcHHFP3ofbHUX7SzILUq4aV0niHe+UA9Da4UCgYBPTGi8uU/nrZ/MCTydg+4r
KE9udwaXMuEHppzC62RKI5TwsU8U3p26kFzNFBVZtBuXc/aPT2roEme6ZcaFAn9t
W3tKnorUI1+0Bq4aLAa1UHNN1xwlDyA70R7CUFKxvUGeye/z2NRllafHjiV/UDnI
0Ael/gHjW0NvvuVt4sjrqQKBgQC7ZYhrXTUNebAFHRuzScH7aXjBjNpyaOLiW2Fh
zTM1ws7dNw5bPI8bD6xJ6ufVS2mdEPnqT7AHr/o194lERHwZkq8l6UmI2tARvEYl
3Fn5IxD3gURFSvqD/SoJys5MNL1+qo11MSL3ZUZqwhhBQixWv2ynCd4HGpCP1cxf
YlfkbQKBgAn68fQhCI8r71UHpW2itvX+5CwC/Lu21LFq0tL8ihvds048cDmSuDcQ
eFvLz/s4ZQ61Wxa1+myNnTEaD49NeUNhvgfqvJ/6XitynwUAZM54q674q6v8mIDg
D73yRq3lPsjbdBVRbhDL03HDHFw5LtXcblaTZF8qnod+Ss6ByUqB
-----END RSA PRIVATE KEY-----`

const railsVectorEnvelope = `{"v":1,"alg":"RSA-OAEP-256+A256GCM","ek":"dPsGSmQYiUajA2jb9E3nO+rojgcMcHVVaCc1jZRzE/yqio8b9lRLw1r3/et232/QX5tQInrWOI3ZiHpoZJoFhN9TnbEPKaX0ScMJW3SwcY/a/sJLxf2FyqZ/RygJiEF/6I4OuUE+aZpt+Tem84WqubMc9+P84OKzgGHBagM3c1k859+EDH+9GDgpiaXkqZqELmkCT7IE0pHhnvyiolwz5YjB8uyEnUvs8RYT/d/hafvCPlJm4PuCYNmMatwsWYtSpeUtP6zaUL9gP61kilKSHsiZ7M0g1euM3x9kvL9Hc8tMAqa6xWC35RMM1vkq3rzI+5yreFDJ3+VaQZy+EDHnZg==","iv":"gl7mO0F3Dj7oOHUw","ct":"PDmLrboQenuSIGQsQfrbqtsDKbWdoVtFxggxS0Q6QbH8VLkXf02VpqXZKc2pZdt6j0VrlPhi/Z4SGlxkTC1KccvBep2NERLCAixIZhFEvPP5CAIE1oTo9dEc413hivAbZ9oepX5/VksSlNpUi5sXAF8CQH8="}`

func TestSealedEnvOpensAnEnvelopeTheControlPlaneSealed(t *testing.T) {
	block, _ := pem.Decode([]byte(railsVectorKey))
	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		t.Fatalf("parse key: %v", err)
	}

	env, err := OpenSealedEnv(railsVectorEnvelope, "tsk_rails_vector", key)

	if err != nil {
		t.Fatalf("open: %v", err)
	}
	want := "-----BEGIN PRIVATE KEY-----\nfrom-rails\n-----END PRIVATE KEY-----"
	if env["SELFHOST_SEALED_TLS_KEY_PEM"] != want {
		t.Fatalf("env = %v", env)
	}
	if _, err := OpenSealedEnv(railsVectorEnvelope, "tsk_other", key); err == nil {
		t.Fatal("the vector opened for another task")
	}
}
