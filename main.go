package main

import (
	"context"
	"crypto/ecdsa"
	"log"
	"math/big"
	"os"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/joho/godotenv"

	generated "eabz/pancakeswap-buybot/generated"
)

const (
	PANCAKESWAP_FACTORY = "0x6725F303b657a9451d8BA641348b6761A6CC7a17"
	PANCAKESWAP_ROUTER  = "0xD99D1c33F9fC3444f8101754aBC46c52416550D1"
	WBNB_ADDRESS        = "0xae13d989daC2f0dEbFf460aC112a837C89BAa7cd"

	tradeAmountWei = 5000000000000000 // 0.005 WBNB in wei
	slippageBps    = 500              // 5% slippage tolerance
)

func purchaseToken(
	ctx context.Context,
	client *ethclient.Client,
	router *generated.UniswapV2Router,
	privateKey *ecdsa.PrivateKey,
	chainID *big.Int,
	event *generated.UniswapV2FactoryPairCreated,
) {
	log.Println("==> Executing buy order...")

	wbnb := common.HexToAddress(WBNB_ADDRESS)

	var token common.Address
	switch {
	case event.Token0 == wbnb:
		token = event.Token1
	case event.Token1 == wbnb:
		token = event.Token0
	default:
		log.Println("pair does not involve wbnb, skipping")
		return
	}

	path := []common.Address{wbnb, token}
	amountIn := big.NewInt(tradeAmountWei)

	callCtx, callCancel := context.WithTimeout(ctx, 10*time.Second)
	defer callCancel()

	amountsOut, err := router.GetAmountsOut(&bind.CallOpts{Context: callCtx}, amountIn, path)
	if err != nil {
		log.Println("failed to fetch quote:", err)
		return
	}

	if len(amountsOut) < 2 {
		log.Println("insufficient quote data (likely no liquidity), skipping")
		return
	}

	expectedOut := new(big.Int).Set(amountsOut[len(amountsOut)-1])
	if expectedOut.Sign() == 0 {
		log.Println("zero expected output, skipping")
		return
	}

	slippage := new(big.Int).Mul(expectedOut, big.NewInt(slippageBps))
	slippage.Div(slippage, big.NewInt(10000))

	amountOutMin := new(big.Int).Sub(expectedOut, slippage)
	if amountOutMin.Sign() <= 0 {
		log.Println("slippage tolerance too high, skipping")
		return
	}

	erc20, err := generated.NewErc20(token, client)
	if err != nil {
		log.Println("unable to initialize erc20 instance")
	}

	symbol, err := erc20.Symbol(nil)
	if err != nil {
		log.Println("unable to get erc20 symbol")
	}

	log.Println("Buying:", symbol)
	log.Println("Amount: 0.05 WBNB")
	log.Println("Slippage: 0.05 WBNB")

	log.Printf("==> expected out: %s | min out after %.2f%% slippage: %s", expectedOut.String(), float64(slippageBps)/100, amountOutMin.String())

	auth, err := bind.NewKeyedTransactorWithChainID(privateKey, chainID)
	if err != nil {
		log.Println("==> failed to create transactor:", err)
		return
	}

	txCtx, txCancel := context.WithTimeout(ctx, 30*time.Second)
	defer txCancel()

	auth.Context = txCtx
	auth.Value = amountIn

	gasPrice, err := client.SuggestGasPrice(txCtx)
	if err != nil {
		log.Println("==> failed to suggest gas price:", err)
		return
	}

	auth.GasPrice = gasPrice

	deadline := big.NewInt(time.Now().Add(3 * time.Minute).Unix())

	tx, err := router.SwapExactETHForTokens(auth, amountOutMin, path, auth.From, deadline)
	if err != nil {
		log.Println("==> swap failed:", err)
		return
	}

	log.Println("==> swap tx submitted:", tx.Hash().Hex())
}

func main() {
	err := godotenv.Load()
	if err != nil {
		log.Fatal("Error loading .env file")
	}

	log.Println("==> Starting PancakSwap V2 Buy Bot")

	log.Println("==> Initialize RPC listener")
	rpcUrl := os.Getenv("RPC_URL")

	client, err := ethclient.Dial(rpcUrl)
	if err != nil {
		log.Fatal(err)
	}

	ctx := context.Background()

	privateKeyHex := strings.TrimPrefix(os.Getenv("PRIVATE_KEY"), "0x")
	if privateKeyHex == "" {
		log.Fatal("PRIVATE_KEY is not set in the environment")
	}

	privateKey, err := crypto.HexToECDSA(privateKeyHex)
	if err != nil {
		log.Fatalf("invalid private key: %v", err)
	}

	chainID, err := client.ChainID(ctx)
	if err != nil {
		log.Fatal(err)
	}

	router, err := generated.NewUniswapV2Router(common.HexToAddress(PANCAKESWAP_ROUTER), client)
	if err != nil {
		log.Fatal(err)
	}

	pancakeSwapFactoryAddress := common.HexToAddress(PANCAKESWAP_FACTORY)

	factoryABI, err := generated.UniswapV2FactoryMetaData.GetAbi()
	if err != nil {
		log.Fatal(err)
	}

	filterQuery := ethereum.FilterQuery{
		Addresses: []common.Address{pancakeSwapFactoryAddress},
		Topics:    [][]common.Hash{{factoryABI.Events["PairCreated"].ID}},
	}

	newPairLogChannel := make(chan types.Log)

	log.Println("==> Listening to new pair events...")

	sub, err := client.SubscribeFilterLogs(ctx, filterQuery, newPairLogChannel)
	if err != nil {
		log.Fatal(err)
	}

	factoryFilterer, err := generated.NewUniswapV2FactoryFilterer(pancakeSwapFactoryAddress, client)
	if err != nil {
		log.Fatal(err)
	}

	for {
		select {
		case err := <-sub.Err():
			log.Fatal(err)
		case vLog := <-newPairLogChannel:
			log.Println("")
			log.Println("==> New pair detected...")

			event, err := factoryFilterer.ParsePairCreated(vLog)
			if err != nil {
				log.Println("==> failed to parse event:", err)
				continue
			}

			log.Println("==> block: ", vLog.BlockNumber)
			log.Println("==> transaction: ", vLog.TxHash.Hex())
			log.Println("==> token0: ", event.Token0)
			log.Println("==> token1: ", event.Token1)
			log.Println("==> pair: ", event.Pair)
			log.Println("==> pair index: ", event.Arg3)

			log.Println("")

			go purchaseToken(ctx, client, router, privateKey, chainID, event)
		}
	}
}
