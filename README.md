
# Hostlink

It makes managing software on your machines very easy.

## Installation

Just like it, the installation is also very simple.

```sh
curl -fsSL https://raw.githubusercontent.com/selfhost-dev/hostlink/refs/heads/main/scripts/linux/install.sh | sudo sh
```

Just running this above script will make the server up and running on your
machine. You can access it on port :1232 of your machine.

## Upcoming Features

- Agent self update
- Passing parameter in script
- Registering agent through webhook call
- Following a workflow from the remote control plane
- Following multiple workflow from the remote control plane
- Adding support for migrations in workflow
- Adding support to revert migrations for any issue
- Registering multiple agents to the same workflow
- Task scheduling for any future date
- CRUD on the future task from the control plane
