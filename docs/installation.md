# Agent Install

We are going to cover how the installation of the agent will look like for
someone who is trying to install it for the first time.

## Installation with register

The future development of the agent should also takes care of registering the
system to a remote control plane. This control plane can send the commands to
execute by the agent to get the desired state.

The binary can run in agent mode or control plane mode.

At this point, there is no separation between both of the modes. Ideally there
should be a separate processes so that both can interact with each other. I
guess we need to have two services files which we can install using systemd.

