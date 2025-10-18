package main

import (
	"context"
	"log"
	"os"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/joho/godotenv"

	generated "eabz/pancakeswap-buybot/generated"
)

const (
	PANCAKESWAP_FACTORY = "0x6725F303b657a9451d8BA641348b6761A6CC7a17"
	WBNB_ADDRESS        = "0xae13d989daC2f0dEbFf460aC112a837C89BAa7cd"
)

func purchaseToken(event *generated.UniswapV2FactoryPairCreated) {
	log.Println("==> Executing buy order...")

	token := event.Token0
	if token == common.HexToAddress(WBNB_ADDRESS) {
		token = event.Token1
	}

	log.Println("==> target token: ", token.Hex())
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

	sub, err := client.SubscribeFilterLogs(context.Background(), filterQuery, newPairLogChannel)
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

			go purchaseToken(event)
		}
	}
}
