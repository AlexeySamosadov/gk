# Gonka TestNet

Gonka TestNet is a chain for testing upgrades and parameter changes without risk. The goal is to keep it alive all the time to maintain state similar to mainnet and run all upgrades on this testnet first. 

Contributors can request direct access to this TestNet for experiments.

## Rules

- TestNet must be deployed from the `testnet` branch of the repo
- The `test-net-cloud/nebius` directory of the `testnet` branch must have scripts that automatically restart testnet at the current mainnet version
- Developers shouldn't redeploy testnet from their branch as it might block work of other contributors. Any deployment changes must be discussed explicitly
- There is no limit on using, running upgrades or changing params when the chain is running, but it might be useful to notify colleagues about major changes to avoid conflicts 

## How to deploy

The deployment process uses a set of scripts to initialize a genesis node and two validator nodes. The key files are:
- `prepare.sh` - copy deployment files to the servers.
- `launch.py` -  main script to launch a node in `genesis` or `join` mode.
- `join-1.sh` & `join-2.sh` - wrapper scripts to launch validator nodes.
- `genesis-overrides.json` - genesis configuration.
- `create-infra.sh`: (Optional) Sets up cloud infrastructure on Nebius from scratch.

The deployment uses the following pre-configured servers:
- **Genesis:** `89.169.111.79`
- **Join 1:** `89.169.110.61`
- **Join 2:** `89.169.110.250`

*Note: If you need to set up this infrastructure from scratch, use the `create-infra.sh` script first.*

### Copy Files to Servers

From your local machine, run `prepare.sh` to copy the necessary files to all servers.
```bash
SSH_KEY_PATH=~/.ssh/your_key ./prepare.sh
```

### Launch Genesis Node

SSH into the genesis server and run `launch.py` in `genesis` mode.
```bash
# On 89.169.111.79
python3 launch.py --mode genesis --branch origin/testnet
```

### Launch Join Nodes
SSH into the other two servers and run their respective join scripts.
```bash
# On 89.169.110.61
./join-1.sh
```
```bash
# On 89.169.110.250
./join-2.sh
```