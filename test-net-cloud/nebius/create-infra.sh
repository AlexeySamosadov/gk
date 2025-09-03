# Prerequisites:
# - Nebius CLI installed and configured with your credentials
# - jq installed for JSON parsing
# - Set project ID in nebuius CLI:
#   nebius config set parent-id project-e00pbskken10vwe2ptydhw

# Create a new network and subnet in Nebius cloud
NETWORK_NAME="testnet-network"
SUBNET_NAME="testnet-subnet"
export NB_NETWORK_ID=$(nebius vpc network create \
   --name "$NETWORK_NAME" \
   --format json | jq -r ".metadata.id")
export NB_SUBNET_ID=$(nebius vpc subnet create \
   --name "$SUBNET_NAME" \
   --network-id "$NB_NETWORK_ID" \
   --format json | jq -r ".metadata.id")
