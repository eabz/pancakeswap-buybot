package main

import (
	"context"
	"crypto/ecdsa"
	"errors"
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
	"github.com/shopspring/decimal"

	generated "eabz/pancakeswap-buybot/generated"
)

const (
	PANCAKESWAP_FACTORY = "0x6725F303b657a9451d8BA641348b6761A6CC7a17"
	PANCAKESWAP_ROUTER  = "0xD99D1c33F9fC3444f8101754aBC46c52416550D1"
	WBNB_ADDRESS        = "0xae13d989daC2f0dEbFf460aC112a837C89BAa7cd"

	tradeAmountWei    = 500_000_000_000_000 // 0.0005 WBNB in wei
	maxPriceImpactBps = 1500                // 15% maximum allowed price impact
	minSlippageBps    = 50                  // 0.50% minimum slippage
	safetyBufferBps   = 10                  // +0.10% buffer on top of price impact
)

func weiToDecimal(ivalue interface{}, decimals int) decimal.Decimal {
	value := new(big.Int)
	switch v := ivalue.(type) {
	case string:
		value.SetString(v, 10)
	case *big.Int:
		value = v
	}

	mul := decimal.NewFromFloat(float64(10)).Pow(decimal.NewFromFloat(float64(decimals)))
	num, _ := decimal.NewFromString(value.String())
	result := num.Div(mul)

	return result.Round(6)
}

func computePriceImpactBps(amountIn, amountOut, reserveIn, reserveOut *big.Int) int64 {
	if amountIn.Sign() <= 0 || amountOut.Sign() <= 0 || reserveIn.Sign() <= 0 || reserveOut.Sign() <= 0 {
		return 0
	}

	midPrice := new(big.Rat).SetFrac(reserveOut, reserveIn)
	execPrice := new(big.Rat).SetFrac(amountOut, amountIn)

	ratio := new(big.Rat).Quo(execPrice, midPrice)
	impact := new(big.Rat).Sub(new(big.Rat).SetInt64(1), ratio)

	if impact.Sign() < 0 {
		return 0
	}

	bpsRat := new(big.Rat).Mul(impact, new(big.Rat).SetInt64(10000))

	bpsInt, _ := bpsRat.Float64()
	if bpsInt < 0 {
		return 0
	}

	return int64(bpsInt)
}

func applySlippage(amount *big.Int, slippageBps int64) (*big.Int, error) {
	if amount.Sign() <= 0 {
		return nil, errors.New("amount must be positive")
	}

	if slippageBps < 0 || slippageBps >= 10000 {
		return nil, errors.New("invalid slippage basis points")
	}

	scale := big.NewInt(10000 - slippageBps)
	result := new(big.Int).Mul(amount, scale)
	return result.Div(result, big.NewInt(10000)), nil
}

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

	symbol := token.Hex()
	decimals := uint8(18)
	if erc20, err := generated.NewErc20(token, client); err == nil {
		if sym, err := erc20.Symbol(nil); err == nil && sym != "" {
			symbol = sym
		}
		if dec, err := erc20.Decimals(nil); err == nil {
			decimals = dec
		}
	}

	path := []common.Address{wbnb, token}
	amountIn := big.NewInt(tradeAmountWei)

	pairInstance, err := generated.NewUniswapV2Pair(event.Pair, client)
	if err != nil {
		log.Println("failed to initialize pair instance:", err)
		return
	}

	reserveCtx, reserveCancel := context.WithTimeout(ctx, 10*time.Second)
	reserves, err := pairInstance.GetReserves(&bind.CallOpts{Context: reserveCtx})
	reserveCancel()
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

	quoteCtx, quoteCancel := context.WithTimeout(ctx, 10*time.Second)
	amountOut, err := router.GetAmountOut(&bind.CallOpts{Context: quoteCtx}, amountIn, reserveIn, reserveOut)
	quoteCancel()
	if err != nil {
		log.Println("failed to fetch quote:", err)
		return
	}

	expectedOut := new(big.Int).Set(amountOut)

	piBps := computePriceImpactBps(amountIn, expectedOut, reserveIn, reserveOut)
	log.Printf("==> Price impact: %d bps (%.2f%%)", piBps, float64(piBps)/100.0)
	if piBps > maxPriceImpactBps {
		log.Println("price impact exceeds limit (>", maxPriceImpactBps, "bps), skipping trade")
		return
	}

	slippageBps := min(max(int64(piBps)+safetyBufferBps, minSlippageBps), maxPriceImpactBps)

	amountOutMin, err := applySlippage(expectedOut, slippageBps)
	if err != nil {
		log.Println("failed to apply slippage:", err)
		return
	}

	if amountOutMin.Sign() <= 0 {
		log.Println("computed minimum output non-positive, skipping")
		return
	}

	log.Printf("==> Expected out: %s %s | min out (slippage %d bps): %s %s", weiToDecimal(expectedOut, int(decimals)), symbol, slippageBps, weiToDecimal(amountOutMin, int(decimals)), symbol)
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
	auth.GasLimit = 200000

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
	log.Printf("==> Tokens received: %s %s", weiToDecimal(amountOutMin, int(decimals)), symbol)
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
