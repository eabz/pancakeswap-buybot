package main

import (
	"context"
	"crypto/ecdsa"
	"log"
	"math"
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

	tradeAmountWei   = 500000000000000 // 0.0005 WBNB in wei
	slippageLimitBps = 2000            // max tolerated slippage (20%)
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

	pairInstance, err := generated.NewUniswapV2Pair(event.Pair, client)
	if err != nil {
		log.Println("failed to initialize pair instance:", err)
		return
	}

	reserves, err := pairInstance.GetReserves(nil)
	if err != nil {
		log.Println("failed to fetch pair reserves:", err)
		return
	}

	var reserveIn, reserveOut *big.Int
	if event.Token0 == wbnb {
		reserveIn = reserves.Reserve0
		reserveOut = reserves.Reserve1
	} else {
		reserveIn = reserves.Reserve1
		reserveOut = reserves.Reserve0
	}

	if reserveIn.Sign() == 0 || reserveOut.Sign() == 0 {
		log.Println("pair has no liquidity, skipping")
		return
	}

	if reserveIn.Cmp(amountIn) <= 0 {
		log.Println("trade size exceeds available liquidity, skipping")
		return
	}

	amountsOut, err := router.GetAmountsOut(nil, amountIn, path)
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

	reserveInPost := new(big.Int).Add(reserveIn, amountIn)
	reserveOutPost := new(big.Int).Sub(reserveOut, expectedOut)
	if reserveOutPost.Sign() <= 0 {
		log.Println("resulting reserves invalid, skipping")
		return
	}

	prePrice := new(big.Float).Quo(new(big.Float).SetInt(reserveOut), new(big.Float).SetInt(reserveIn))
	postPrice := new(big.Float).Quo(new(big.Float).SetInt(reserveOutPost), new(big.Float).SetInt(reserveInPost))
	if prePrice.Sign() == 0 {
		log.Println("unable to compute price impact, skipping")
		return
	}

	priceImpact := new(big.Float).Quo(new(big.Float).Sub(prePrice, postPrice), prePrice)
	priceImpactFloat, _ := priceImpact.Abs(priceImpact).Float64()
	priceImpactPercent := priceImpactFloat * 100
	priceImpactBps := int64(math.Round(priceImpactFloat * 10000))

	if priceImpactBps > slippageLimitBps {
		log.Printf("skipping trade due to high slippage: %.2f%%", priceImpactPercent)
		return
	}

	slippageAmount := new(big.Int).Mul(expectedOut, big.NewInt(priceImpactBps))
	slippageAmount.Div(slippageAmount, big.NewInt(10000))

	amountOutMin := new(big.Int).Sub(expectedOut, slippageAmount)
	if amountOutMin.Sign() <= 0 {
		log.Println("computed minimum output non-positive, skipping")
		return
	}

	log.Printf("==> Expected out: %s (slippage: %.2f%%)", expectedOut.String(), priceImpactPercent)

	auth, err := bind.NewKeyedTransactorWithChainID(privateKey, chainID)
	if err != nil {
		log.Println("failed to create transactor:", err)
		return
	}

	txCtx, txCancel := context.WithTimeout(ctx, 30*time.Second)
	defer txCancel()

	auth.Context = txCtx
	auth.Value = amountIn

	gasPrice, err := client.SuggestGasPrice(txCtx)
	if err != nil {
		log.Println("failed to suggest gas price:", err)
		return
	}

	auth.GasPrice = gasPrice

	deadline := big.NewInt(time.Now().Add(3 * time.Minute).Unix())

	tx, err := router.SwapExactETHForTokens(auth, amountOutMin, path, auth.From, deadline)
	if err != nil {
		log.Println("swap failed:", err)
		return
	}

	receipt, err := bind.WaitMined(context.Background(), client, tx)
	if err != nil {
		log.Println("unable to get transaction receipt")
	}

	log.Println("")
	log.Println("==> Buy transaction successful")
	log.Println("==> Tx Hash:", tx.Hash().Hex())
	log.Println("==> Tokens received:", amountOutMin)
	log.Println("==> Gas used:", receipt.GasUsed)
	if receipt.Status == 1 {
		log.Println("==> Status: succeed")
	} else {
		log.Println("==> Status: failed")
	}

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
