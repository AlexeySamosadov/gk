// Deployment script for BridgeContract
// Usage: npx hardhat run deploy.js --network <network>

import hre from "hardhat";
import { ethers } from "ethers";
import dotenv from "dotenv";

// Load environment variables from .env file
dotenv.config();

// Helper function to get provider and signer for current network
async function getProviderAndSigner() {
    const networkConnection = await hre.network.connect();
    const networkName = networkConnection.networkName;
    
    // Get RPC URL for the network
    let rpcUrl;
    let signer;
    
    if (networkName === "localhost" || networkName === "hardhat") {
        rpcUrl = "http://127.0.0.1:8545";
        const provider = new ethers.JsonRpcProvider(rpcUrl);
        signer = await provider.getSigner();
        return { provider, signer, ethers };
    } else {
        // Remote network - use private key from env
        rpcUrl = process.env[`${networkName.toUpperCase()}_RPC_URL`];
        if (!rpcUrl) {
            throw new Error(`RPC URL not found for network ${networkName}. Set ${networkName.toUpperCase()}_RPC_URL in your .env file.`);
        }
        
        const privateKey = process.env.PRIVATE_KEY;
        if (!privateKey) {
            throw new Error(`PRIVATE_KEY not found in environment. Set PRIVATE_KEY in your .env file.`);
        }
        
        const provider = new ethers.JsonRpcProvider(rpcUrl);
        signer = new ethers.Wallet(privateKey, provider);
        return { provider, signer, ethers };
    }
}

async function main() {
    console.log("Deploying BridgeContract...");
    
    // Get provider and signer
    const { provider, signer, ethers } = await getProviderAndSigner();
    console.log("Deploying from:", await signer.getAddress());
    
    // Load compiled contract artifacts
    const artifact = await hre.artifacts.readArtifact("BridgeContract");
    
    // Create contract factory
    const BridgeContract = new ethers.ContractFactory(artifact.abi, artifact.bytecode, signer);

    // Define chain IDs for cross-chain replay protection
    // Gonka chain identifier (using sha256 for unique chain ID)
    const gonkaChainId = ethers.sha256(ethers.toUtf8Bytes("gonka-mainnet"));
    
    // Ethereum chain ID (convert network chain ID to bytes32)
    const network = await provider.getNetwork();
    const ethereumChainId = ethers.zeroPadValue(ethers.toBeHex(network.chainId), 32);
    
    console.log("Chain IDs:");
    console.log("- Gonka Chain ID:", gonkaChainId);
    console.log("- Ethereum Chain ID:", ethereumChainId, "(chain", network.chainId + ")");

    // Deploy the contract with required constructor arguments
    const bridge = await BridgeContract.deploy(gonkaChainId, ethereumChainId);
    
    console.log("Waiting for deployment...");
    await bridge.waitForDeployment();

    const contractAddress = await bridge.getAddress();
    console.log("BridgeContract deployed to:", contractAddress);
    console.log("Transaction hash:", bridge.deploymentTransaction().hash);

    // Verify the initial state
    const currentState = await bridge.getCurrentState();
    const latestEpoch = await bridge.getLatestEpochInfo();
    
    console.log("\nInitial State:");
    console.log("- Contract State:", currentState === 0 ? "ADMIN_CONTROL" : "NORMAL_OPERATION");
    console.log("- Latest Epoch ID:", latestEpoch.epochId.toString());
    console.log("- Contract Owner:", await bridge.owner());

    console.log("\nNext Steps:");
    console.log("1. Submit genesis epoch (epoch 1) group key:");
    console.log("   bridge.submitGroupKey(1, genesisGroupKey, '0x')");
    console.log("2. Reset to normal operation:");
    console.log("   bridge.resetToNormalOperation()");

    // Return contract instance for further operations
    return bridge;
}

// Example usage functions for testing
async function submitGenesisEpoch(bridgeAddress, groupPublicKey) {
    const { provider, signer, ethers } = await getProviderAndSigner();
    const artifact = await hre.artifacts.readArtifact("BridgeContract");
    const BridgeContract = new ethers.ContractFactory(artifact.abi, artifact.bytecode, signer);
    const bridge = BridgeContract.attach(bridgeAddress);

    console.log("Submitting genesis epoch (epoch 1)...");
    
    const tx = await bridge.submitGroupKey(
        1, // epochId
        groupPublicKey, // 96-byte G2 public key
        "0x" // empty validation signature for genesis
    );
    
    await tx.wait();
    console.log("Genesis epoch submitted! Transaction:", tx.hash);

    return tx;
}

async function enableNormalOperation(bridgeAddress) {
    const { provider, signer, ethers } = await getProviderAndSigner();
    const artifact = await hre.artifacts.readArtifact("BridgeContract");
    const BridgeContract = new ethers.ContractFactory(artifact.abi, artifact.bytecode, signer);
    const bridge = BridgeContract.attach(bridgeAddress);

    console.log("Enabling normal operation...");
    
    const tx = await bridge.resetToNormalOperation();
    await tx.wait();
    
    console.log("Normal operation enabled! Transaction:", tx.hash);
    
    const newState = await bridge.getCurrentState();
    console.log("Current state:", newState === 0 ? "ADMIN_CONTROL" : "NORMAL_OPERATION");

    return tx;
}

// Example withdrawal function for testing
async function testWithdrawal(bridgeAddress, withdrawalCommand) {
    const { provider, signer, ethers } = await getProviderAndSigner();
    const artifact = await hre.artifacts.readArtifact("BridgeContract");
    const BridgeContract = new ethers.ContractFactory(artifact.abi, artifact.bytecode, signer);
    const bridge = BridgeContract.attach(bridgeAddress);

    console.log("Testing withdrawal...");
    console.log("Command:", withdrawalCommand);

    try {
        const tx = await bridge.withdraw(withdrawalCommand);
        await tx.wait();
        console.log("Withdrawal successful! Transaction:", tx.hash);
        return tx;
    } catch (error) {
        console.error("Withdrawal failed:", error.message);
        throw error;
    }
}

// Helper function to create example withdrawal command
function createWithdrawalCommand(epochId, requestId, recipient, tokenContract, amount) {
    return {
        epochId: epochId,
        requestId: requestId,
        recipient: recipient,
        tokenContract: tokenContract,
        amount: amount,
        signature: "0x" + "00".repeat(48) // Placeholder - replace with actual BLS signature
    };
}

// Example BLS group public key (placeholder - replace with actual key)
const EXAMPLE_GROUP_PUBLIC_KEY = "0x" + "00".repeat(96);

// Export functions for use in other scripts
export {
    main,
    getProviderAndSigner,
    submitGenesisEpoch,
    enableNormalOperation,
    testWithdrawal,
    createWithdrawalCommand,
    EXAMPLE_GROUP_PUBLIC_KEY
};

// Run deployment if script is executed directly
// Note: When using 'npx hardhat run', the script is always executed
main()
    .then(() => process.exit(0))
    .catch((error) => {
        console.error(error);
        process.exit(1);
    });
