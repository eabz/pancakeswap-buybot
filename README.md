# PancakeSwap Buy Bot

This project listens for newly created PancakeSwap V2 pairs and, when the WBNB side of the pair is detected, attempts to buy the partner token with a fixed WBNB amount. The trade is executed through the router using a constant slippage buffer to compute `amountOutMin` before submission.

> **Warning**
> This bot is created for testing purposes only. It is not optimized for mainnet or real trading, and using real funds may result in losing money.

## Features

- Subscribes to the factory’s `PairCreated` event via go-ethereum filter streams.
- Loads shared ABIs from generated Go bindings (`generated/`).
- Computes live reserves through the pair contract to quote the output for the fixed trade size.
- Applies a fixed 5 % slippage buffer when computing `amountOutMin`.
- Executes `swapExactETHForTokens` on PancakeSwap V2 using an account private key supplied through `.env`.

## Requirements

- Go 1.25+
- `abigen` from go-ethereum (`go install github.com/ethereum/go-ethereum/cmd/abigen@latest`)
- A BSC RPC endpoint with websocket support.
- A funded wallet private key (exported in `.env` – protect this file!).

## Project Layout

- `main.go` – entrypoint, log subscription, reserve/quote lookups, fixed-slippage trade execution.
- `generated/` – Go bindings produced by `abigen` (`UniswapV2Factory`, `UniswapV2Router`, `UniswapV2Pair`, `Erc20`).
- `abis/` – canonical ABI JSON sources.
- `.env.example` – required environment variables.

## Setup

1. **Copy environment template**  
   ```bash
   cp .env.example .env
   ```

2. **Edit `.env`**  
   | Variable | Description |
   |----------|-------------|
   | `RPC_URL` | WebSocket RPC endpoint (e.g. `wss://bsc-testnet.nodereal.io/ws/v1/...`). |
   | `PRIVATE_KEY` | Hex-encoded private key (with or without `0x`). Never commit this file. |

3. **Install dependencies**  
   ```bash
   go mod tidy
   ```

4. **Generate Go bindings (only required if ABI changes)**  
   ```bash
   abigen --abi abis/uniswap-v2-router.json  --pkg generated --type UniswapV2Router --out generated/UniswapV2Router.go
   abigen --abi abis/uniswap-v2-factory.json --pkg generated --type UniswapV2Factory --out generated/UniswapV2Factory.go
   abigen --abi abis/uniswap-v2-pair.json    --pkg generated --type UniswapV2Pair    --out generated/UniswapV2Pair.go
   abigen --abi abis/erc20.json              --pkg generated --type Erc20            --out generated/Erc20.go
   ```

## Running

```bash
go run .
```

The process will:

1. Load `.env` and connect to the RPC endpoint.
2. Subscribe to `PairCreated` events.
3. For each WBNB pair:
   - Fetch reserves from the pair contract.
   - Quote the output via the router.
   - Apply a fixed 5 % slippage buffer to derive `amountOutMin`.
   - Submit `swapExactETHForTokens` swapping exactly **0.0005 WBNB**.

Console logs include block/transaction identifiers, reserves, expected output, computed slippage, and transaction hashes for successful swaps.

## Configuration

Trade constants live in `main.go`:

| Constant | Default | Description |
|----------|---------|-------------|
| `tradeAmountWei` | `0.0005 WBNB` | Fixed input amount for each swap. |
| `fixedSlippageBps` | `500` | Constant slippage buffer (5 %) used to compute `amountOutMin`. |

Adjust and rebuild if you need different parameters.

## Development

- Format + lint:
  ```bash
  gofmt -w main.go
  ```
- Build:
  ```bash
  go build ./...
  ```
- Use your own fork / private key for testing; consider running against a mainnet fork (e.g., Hardhat, Anvil) to avoid real losses.
