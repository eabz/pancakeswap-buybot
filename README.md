# PancakeSwap Buy Bot

A bot that listen to new created pairs for pancake swap v2 and executes a fixed amount trade.

## Requirements

- Golang
- Abigen

## Generate ABI files

abigen --abi abis/uniswap-v2-router.json  --pkg generated --type UniswapV2Router --out generated/UniswapV2Router.go
abigen --abi abis/uniswap-v2-factory.json  --pkg generated --type UniswapV2Factory --out generated/UniswapV2Factory.go
abigen --abi abis/erc20.json  --pkg generated --type Erc20 --out generated/Erc20.go

## Build

## Run

