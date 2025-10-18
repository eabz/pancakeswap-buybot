

## Generate ABI files

abigen --abi abis/uniswap-v2-router.json  --pkg main --type UniswapV2Router --out generated/UniswapV2Router.go
abigen --abi abis/uniswap-v2-factory.json  --pkg main --type UniswapV2Factory --out generated/UniswapV2Factory.go
abigen --abi abis/erc20.json  --pkg main --type Erc20 --out generated/Erc20.go