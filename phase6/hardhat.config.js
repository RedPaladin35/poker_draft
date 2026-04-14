require("@nomicfoundation/hardhat-toolbox");

/** @type import('hardhat/config').HardhatUserConfig */
module.exports = {
    solidity: {
        version: "0.8.20",
        settings: {
            optimizer: {
                enabled: true,
                runs: 200,
            },
        },
    },
    networks: {
        // Local Hardhat node (default for `npx hardhat test`).
        hardhat: {
            chainId: 31337,
            mining: {
                auto: true,
                interval: 0,
            },
        },
        // Local Anvil node (Foundry). Start with: anvil --port 8545
        anvil: {
            url: "http://127.0.0.1:8545",
            chainId: 31337,
            accounts: {
                // Anvil default mnemonic — only for local dev, never use in production.
                mnemonic: "test test test test test test test test test test test junk",
                count: 10,
            },
        },
        // Sepolia testnet — set SEPOLIA_RPC_URL and PRIVATE_KEY in .env
        sepolia: {
            url: process.env.SEPOLIA_RPC_URL || "",
            accounts: process.env.PRIVATE_KEY ? [process.env.PRIVATE_KEY] : [],
        },
    },
    // Contract verification (optional).
    etherscan: {
        apiKey: process.env.ETHERSCAN_API_KEY || "",
    },
    // Gas reporter (optional, set REPORT_GAS=true).
    gasReporter: {
        enabled: process.env.REPORT_GAS !== undefined,
        currency: "USD",
    },
};
