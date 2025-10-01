
# Hostlink

It makes managing software on your machines very easy.

This project is trying to be an agent process which make managing software on the host machine very easy.

## Installation

Just like it, the installation is also very simple.

```sh
curl -fsSL https://raw.githubusercontent.com/selfhost-dev/hostlink/refs/heads/main/scripts/linux/install.sh | sudo sh
```

Just running this above script will make the server up and running on your
machine. You can access it on port :1232 of your machine.

## Authentication Architecture

Hostlink uses **one-way authentication** for secure agent-to-server communication:

### Agent → Server Authentication

- **Agent signs requests** using RSA-PSS signatures with SHA-256
- **Server verifies signatures** using agent's public key stored in database
- **Timestamp-based replay protection** with 5-minute window (±300 seconds)
- **No nonce storage** required - stateless verification for horizontal scalability

### Server → Agent Communication

- **Server does NOT sign responses** - authentication is one-way only
- **HTTPS/TLS provides transport security** between server and agent

### Authentication Flow

1. **Agent Registration**
   - Agent generates RSA key pair during installation
   - Sends public key to server during registration
   - Server stores public key in database associated with Agent ID

2. **Authenticated Requests**
   - Agent creates message: `AgentID|Timestamp|Nonce`
   - Signs message with private key using RSA-PSS
   - Sends request with headers: `X-Agent-ID`, `X-Timestamp`, `X-Nonce`, `X-Signature`

3. **Server Verification**
   - Retrieves agent's public key from database
   - Verifies timestamp is within 5-minute window
   - Reconstructs message and verifies signature
   - Returns 401 Unauthorized if verification fails

### Security Features

- ✅ Cryptographic agent authentication
- ✅ Replay attack prevention via timestamp validation
- ✅ Stateless verification (no server-side nonce tracking)
- ✅ Horizontally scalable authentication
- ✅ HTTPS/TLS for transport security

## Upcoming Features

- Agent self update
- Passing parameter in script
- Following a workflow from the remote control plane
- Following multiple workflow from the remote control plane
- Adding support for migrations in workflow
- Adding support to revert migrations for any issue
- Registering multiple agents to the same workflow
- Task scheduling for any future date
- CRUD on the future task from the control plane
