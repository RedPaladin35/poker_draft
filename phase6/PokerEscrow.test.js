/**
 * PokerEscrow contract tests.
 * Run with: npx hardhat test
 *
 * Requires:
 *   npm install --save-dev hardhat @nomicfoundation/hardhat-toolbox
 *   npx hardhat init  (choose "empty hardhat.config.js")
 */

const { expect }       = require("chai");
const { ethers }       = require("hardhat");
const { parseEther, keccak256, toUtf8Bytes, solidityPackedKeccak256 } = ethers;

// ── helpers ───────────────────────────────────────────────────────────────────

async function deployEscrow(maxSeats = 3) {
    const [deployer, ...signers] = await ethers.getSigners();
    const Factory = await ethers.getContractFactory("PokerEscrow");
    const contract = await Factory.deploy("table-001", maxSeats);
    await contract.waitForDeployment();
    return { contract, deployer, signers };
}

// Sign an outcome digest using the Ethereum personal_sign convention.
async function signOutcome(signer, tableID, handNum, payoutDeltas, stateRoot) {
    const abiCoder = new ethers.AbiCoder();
    const digest = keccak256(abiCoder.encode(
        ["string", "uint256", "int256[]", "bytes32"],
        [tableID, handNum, payoutDeltas, stateRoot]
    ));
    return signer.signMessage(ethers.getBytes(digest));
}

// ── Tests ─────────────────────────────────────────────────────────────────────

describe("PokerEscrow", function () {

    // ── Deployment ──────────────────────────────────────────────────────────

    describe("deployment", function () {
        it("sets tableID and maxSeats", async function () {
            const { contract } = await deployEscrow(3);
            expect(await contract.tableID()).to.equal("table-001");
            expect(await contract.maxSeats()).to.equal(3);
        });

        it("starts in Open state", async function () {
            const { contract } = await deployEscrow();
            expect(await contract.state()).to.equal(0); // TableState.Open
        });

        it("reverts on invalid seat count", async function () {
            const Factory = await ethers.getContractFactory("PokerEscrow");
            await expect(Factory.deploy("t", 1)).to.be.revertedWith("seats must be 2-9");
            await expect(Factory.deploy("t", 10)).to.be.revertedWith("seats must be 2-9");
        });
    });

    // ── joinTable ────────────────────────────────────────────────────────────

    describe("joinTable", function () {
        it("accepts a valid join with ETH", async function () {
            const { contract, signers } = await deployEscrow(3);
            const alice = signers[0];
            await expect(
                contract.connect(alice).joinTable("QmAlicePeerID", { value: parseEther("0.1") })
            ).to.emit(contract, "PlayerJoined")
             .withArgs(alice.address, "QmAlicePeerID", parseEther("0.1"), 0);

            expect(await contract.playerCount()).to.equal(1);
        });

        it("reverts on zero buy-in", async function () {
            const { contract, signers } = await deployEscrow();
            await expect(
                contract.connect(signers[0]).joinTable("QmPeer", { value: 0 })
            ).to.be.revertedWith("buy-in must be > 0");
        });

        it("reverts on duplicate join", async function () {
            const { contract, signers } = await deployEscrow(3);
            const alice = signers[0];
            await contract.connect(alice).joinTable("QmAlice", { value: parseEther("0.1") });
            await expect(
                contract.connect(alice).joinTable("QmAlice", { value: parseEther("0.1") })
            ).to.be.revertedWith("already seated");
        });

        it("auto-starts game when all seats filled", async function () {
            const { contract, signers } = await deployEscrow(2);
            await contract.connect(signers[0]).joinTable("QmA", { value: parseEther("1") });
            await expect(
                contract.connect(signers[1]).joinTable("QmB", { value: parseEther("1") })
            ).to.emit(contract, "GameStarted");
            expect(await contract.state()).to.equal(1); // TableState.Playing
        });

        it("reverts when table is full", async function () {
            const { contract, signers } = await deployEscrow(2);
            await contract.connect(signers[0]).joinTable("QmA", { value: parseEther("1") });
            await contract.connect(signers[1]).joinTable("QmB", { value: parseEther("1") });
            await expect(
                contract.connect(signers[2]).joinTable("QmC", { value: parseEther("1") })
            ).to.be.revertedWith("wrong table state");
        });
    });

    // ── reportOutcome ────────────────────────────────────────────────────────

    describe("reportOutcome", function () {
        async function setupPlaying(maxSeats = 3) {
            const { contract, signers } = await deployEscrow(maxSeats);
            const players = signers.slice(0, maxSeats);
            for (let i = 0; i < maxSeats; i++) {
                await contract.connect(players[i]).joinTable(
                    `QmPeer${i}`, { value: parseEther("1") }
                );
            }
            // State is now Playing.
            return { contract, players };
        }

        it("distributes ETH on valid outcome with 2/3 signatures", async function () {
            const { contract, players } = await setupPlaying(3);

            // Player 0 wins 0.5 ETH, player 1 loses 0.5 ETH, player 2 breaks even.
            const deltas = [
                parseEther("0.5"),
                parseEther("-0.5"),
                parseEther("0"),
            ];
            const stateRoot = ethers.zeroPadValue(toUtf8Bytes("root"), 32);
            const handNum   = 1n;

            // Need ceil(3*2/3) = 2 signatures.
            const sig0 = await signOutcome(players[0], "table-001", handNum, deltas, stateRoot);
            const sig1 = await signOutcome(players[1], "table-001", handNum, deltas, stateRoot);

            await expect(
                contract.connect(players[0]).reportOutcome(
                    deltas, stateRoot, [sig0, sig1], handNum
                )
            ).to.emit(contract, "OutcomeReported");

            expect(await contract.state()).to.equal(2); // Settled
        });

        it("reverts when chip conservation is violated", async function () {
            const { contract, players } = await setupPlaying(3);
            // These don't sum to zero.
            const deltas    = [parseEther("1"), parseEther("0"), parseEther("0")];
            const stateRoot = ethers.zeroPadValue(toUtf8Bytes("root"), 32);
            const sig0 = await signOutcome(players[0], "table-001", 1n, deltas, stateRoot);
            const sig1 = await signOutcome(players[1], "table-001", 1n, deltas, stateRoot);

            await expect(
                contract.connect(players[0]).reportOutcome(deltas, stateRoot, [sig0, sig1], 1n)
            ).to.be.revertedWith("chips not conserved");
        });

        it("reverts with fewer than required signatures", async function () {
            const { contract, players } = await setupPlaying(3);
            const deltas    = [parseEther("0.5"), parseEther("-0.5"), parseEther("0")];
            const stateRoot = ethers.zeroPadValue(toUtf8Bytes("root"), 32);
            // Only 1 sig — need 2.
            const sig0 = await signOutcome(players[0], "table-001", 1n, deltas, stateRoot);

            await expect(
                contract.connect(players[0]).reportOutcome(deltas, stateRoot, [sig0], 1n)
            ).to.be.revertedWith("insufficient signatures");
        });

        it("reverts with tampered state root signatures", async function () {
            const { contract, players } = await setupPlaying(3);
            const deltas   = [parseEther("0"), parseEther("0"), parseEther("0")];
            const realRoot = ethers.zeroPadValue(toUtf8Bytes("real"), 32);
            const fakeRoot = ethers.zeroPadValue(toUtf8Bytes("fake"), 32);

            // Sign the real root, but submit a fake one.
            const sig0 = await signOutcome(players[0], "table-001", 1n, deltas, realRoot);
            const sig1 = await signOutcome(players[1], "table-001", 1n, deltas, realRoot);

            await expect(
                contract.connect(players[0]).reportOutcome(deltas, fakeRoot, [sig0, sig1], 1n)
            ).to.be.revertedWith("not enough valid signatures");
        });
    });

    // ── requiredSignatures ───────────────────────────────────────────────────

    describe("requiredSignatures", function () {
        it("requires ceil(2/3) of players", async function () {
            const cases = [
                { seats: 2, expected: 2n }, // ceil(2*2/3) = 2
                { seats: 3, expected: 2n }, // ceil(3*2/3) = 2
                { seats: 4, expected: 3n }, // ceil(4*2/3) = 3
                { seats: 6, expected: 4n }, // ceil(6*2/3) = 4
                { seats: 9, expected: 6n }, // ceil(9*2/3) = 6
            ];
            for (const { seats, expected } of cases) {
                const { contract, signers } = await deployEscrow(seats);
                for (let i = 0; i < seats; i++) {
                    await contract.connect(signers[i]).joinTable(
                        `QmP${i}`, { value: parseEther("1") }
                    );
                }
                expect(await contract.requiredSignatures()).to.equal(expected);
            }
        });
    });

    // ── markAbandoned / refund ───────────────────────────────────────────────

    describe("abandon and refund", function () {
        it("allows refund after deadline passes", async function () {
            const { contract, signers } = await deployEscrow(2);
            await contract.connect(signers[0]).joinTable("QmA", { value: parseEther("1") });
            await contract.connect(signers[1]).joinTable("QmB", { value: parseEther("1") });

            // Mine enough blocks to pass SETTLEMENT_DEADLINE.
            const deadline = Number(await contract.SETTLEMENT_DEADLINE());
            for (let i = 0; i <= deadline; i++) {
                await ethers.provider.send("evm_mine", []);
            }

            await contract.connect(signers[0]).markAbandoned();
            expect(await contract.state()).to.equal(4); // Abandoned

            const balBefore = await ethers.provider.getBalance(signers[0].address);
            const tx        = await contract.connect(signers[0]).refund();
            const receipt   = await tx.wait();
            const gasUsed   = receipt.gasUsed * receipt.gasPrice;
            const balAfter  = await ethers.provider.getBalance(signers[0].address);

            expect(balAfter + gasUsed).to.be.approximately(
                balBefore + parseEther("1"), parseEther("0.001")
            );
        });

        it("reverts markAbandoned before deadline", async function () {
            const { contract, signers } = await deployEscrow(2);
            await contract.connect(signers[0]).joinTable("QmA", { value: parseEther("1") });
            await contract.connect(signers[1]).joinTable("QmB", { value: parseEther("1") });

            await expect(
                contract.connect(signers[0]).markAbandoned()
            ).to.be.revertedWith("settlement deadline not yet passed");
        });
    });

    // ── playerAt ─────────────────────────────────────────────────────────────

    describe("playerAt", function () {
        it("returns correct player info", async function () {
            const { contract, signers } = await deployEscrow(3);
            await contract.connect(signers[0]).joinTable("QmAlice", { value: parseEther("2") });

            const [addr, peerID, buyIn, withdrawn, slashed] = await contract.playerAt(0);
            expect(addr).to.equal(signers[0].address);
            expect(peerID).to.equal("QmAlice");
            expect(buyIn).to.equal(parseEther("2"));
            expect(withdrawn).to.be.false;
            expect(slashed).to.be.false;
        });

        it("reverts for out-of-range seat", async function () {
            const { contract } = await deployEscrow(3);
            await expect(contract.playerAt(0)).to.be.revertedWith("seat out of range");
        });
    });
});
