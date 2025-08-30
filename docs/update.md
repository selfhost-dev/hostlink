# Agent Updating


This documents covers how you can update the version of the running agent in
place. It's common in software development life cycle that regular update to
your software is push regularly as you get the feedback from the users and any
missing pieces that you encounter. The same is true for this agent. This docs
covers how can you update the agent in regular basis.

At this phase of development we are covering how you can push update manually
to the agent. The automatic update of the system will be coming soon.

If we think carefully there is not much difference between update and install.

In both of the cases the you are trying to have binary up and running which is
not in your system right now. In case of install everything is fresh and new so
decision making would be easier. In case of update you need to make sure that
certain race conditions and execution order is maintained.

