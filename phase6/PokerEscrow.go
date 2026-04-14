// Package abi contains the Go ABI bindings for the PokerEscrow contract.
//
// In production, regenerate this file with:
//
//	solc --abi contracts/PokerEscrow.sol -o contracts/abi/
//	abigen --abi contracts/abi/PokerEscrow.abi \
//	        --pkg abi \
//	        --type PokerEscrow \
//	        --out internal/chain/abi/PokerEscrow.go
//
// This hand-written version mirrors what abigen would produce and is
// used so the Go chain package compiles without the full Solidity toolchain.
package abi

// PokerEscrowABI is the JSON ABI of the PokerEscrow contract.
// Used by go-ethereum's bind package to encode/decode calls and events.
const PokerEscrowABI = `[
  {
    "type": "constructor",
    "inputs": [
      {"name": "_tableID",  "type": "string"},
      {"name": "_maxSeats", "type": "uint8"}
    ],
    "stateMutability": "nonpayable"
  },
  {
    "type": "function",
    "name": "joinTable",
    "inputs": [{"name": "peerID", "type": "string"}],
    "outputs": [],
    "stateMutability": "payable"
  },
  {
    "type": "function",
    "name": "reportOutcome",
    "inputs": [
      {"name": "payoutDeltas", "type": "int256[]"},
      {"name": "_stateRoot",   "type": "bytes32"},
      {"name": "signatures",   "type": "bytes[]"},
      {"name": "handNum",      "type": "uint256"}
    ],
    "outputs": [],
    "stateMutability": "nonpayable"
  },
  {
    "type": "function",
    "name": "submitDispute",
    "inputs": [
      {"name": "accused",    "type": "address"},
      {"name": "reason",     "type": "string"},
      {"name": "evidence",   "type": "bytes"},
      {"name": "accuserSig", "type": "bytes"}
    ],
    "outputs": [],
    "stateMutability": "nonpayable"
  },
  {
    "type": "function",
    "name": "markAbandoned",
    "inputs": [],
    "outputs": [],
    "stateMutability": "nonpayable"
  },
  {
    "type": "function",
    "name": "refund",
    "inputs": [],
    "outputs": [],
    "stateMutability": "nonpayable"
  },
  {
    "type": "function",
    "name": "playerAt",
    "inputs": [{"name": "seat", "type": "uint8"}],
    "outputs": [
      {"name": "addr",      "type": "address"},
      {"name": "peerID",    "type": "string"},
      {"name": "buyIn",     "type": "uint256"},
      {"name": "withdrawn", "type": "bool"},
      {"name": "slashed",   "type": "bool"}
    ],
    "stateMutability": "view"
  },
  {
    "type": "function",
    "name": "playerCount",
    "inputs": [],
    "outputs": [{"name": "", "type": "uint256"}],
    "stateMutability": "view"
  },
  {
    "type": "function",
    "name": "requiredSignatures",
    "inputs": [],
    "outputs": [{"name": "", "type": "uint256"}],
    "stateMutability": "view"
  },
  {
    "type": "function",
    "name": "state",
    "inputs": [],
    "outputs": [{"name": "", "type": "uint8"}],
    "stateMutability": "view"
  },
  {
    "type": "function",
    "name": "tableID",
    "inputs": [],
    "outputs": [{"name": "", "type": "string"}],
    "stateMutability": "view"
  },
  {
    "type": "function",
    "name": "maxSeats",
    "inputs": [],
    "outputs": [{"name": "", "type": "uint8"}],
    "stateMutability": "view"
  },
  {
    "type": "function",
    "name": "totalEscrow",
    "inputs": [],
    "outputs": [{"name": "", "type": "uint256"}],
    "stateMutability": "view"
  },
  {
    "type": "function",
    "name": "stateRoot",
    "inputs": [],
    "outputs": [{"name": "", "type": "bytes32"}],
    "stateMutability": "view"
  },
  {
    "type": "function",
    "name": "gameStartBlock",
    "inputs": [],
    "outputs": [{"name": "", "type": "uint256"}],
    "stateMutability": "view"
  },
  {
    "type": "function",
    "name": "settlementBlock",
    "inputs": [],
    "outputs": [{"name": "", "type": "uint256"}],
    "stateMutability": "view"
  },
  {
    "type": "function",
    "name": "slashEvidence",
    "inputs": [{"name": "", "type": "address"}],
    "outputs": [{"name": "", "type": "bytes"}],
    "stateMutability": "view"
  },
  {
    "type": "function",
    "name": "seatOf",
    "inputs": [{"name": "", "type": "address"}],
    "outputs": [{"name": "", "type": "uint8"}],
    "stateMutability": "view"
  },
  {
    "type": "event",
    "name": "PlayerJoined",
    "inputs": [
      {"name": "player", "type": "address", "indexed": true},
      {"name": "peerID", "type": "string",  "indexed": false},
      {"name": "amount", "type": "uint256", "indexed": false},
      {"name": "seat",   "type": "uint8",   "indexed": false}
    ]
  },
  {
    "type": "event",
    "name": "GameStarted",
    "inputs": [{"name": "blockNumber", "type": "uint256", "indexed": false}]
  },
  {
    "type": "event",
    "name": "OutcomeReported",
    "inputs": [
      {"name": "stateRoot",    "type": "bytes32", "indexed": false},
      {"name": "blockNumber",  "type": "uint256", "indexed": false}
    ]
  },
  {
    "type": "event",
    "name": "PayoutSent",
    "inputs": [
      {"name": "player", "type": "address", "indexed": true},
      {"name": "amount", "type": "uint256", "indexed": false}
    ]
  },
  {
    "type": "event",
    "name": "DisputeFiled",
    "inputs": [
      {"name": "filer",   "type": "address", "indexed": true},
      {"name": "accused", "type": "address", "indexed": true},
      {"name": "reason",  "type": "string",  "indexed": false}
    ]
  },
  {
    "type": "event",
    "name": "SlashExecuted",
    "inputs": [
      {"name": "slashed",       "type": "address", "indexed": true},
      {"name": "slashedAmount", "type": "uint256", "indexed": false},
      {"name": "burnedAmount",  "type": "uint256", "indexed": false}
    ]
  },
  {
    "type": "event",
    "name": "Refunded",
    "inputs": [
      {"name": "player", "type": "address", "indexed": true},
      {"name": "amount", "type": "uint256", "indexed": false}
    ]
  },
  {
    "type": "event",
    "name": "Abandoned",
    "inputs": [{"name": "blockNumber", "type": "uint256", "indexed": false}]
  }
]`

// TableState mirrors the Solidity enum.
type TableState uint8

const (
	TableStateOpen      TableState = 0
	TableStatePlaying   TableState = 1
	TableStateSettled   TableState = 2
	TableStateDisputed  TableState = 3
	TableStateAbandoned TableState = 4
)

func (s TableState) String() string {
	return [...]string{"Open", "Playing", "Settled", "Disputed", "Abandoned"}[s]
}
