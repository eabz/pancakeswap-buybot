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
)

const PANCAKESWAP_FACTORY = "0x6725F303b657a9451d8BA641348b6761A6CC7a17"
const PANCAKESWAP_ROUTER = "0xD99D1c33F9fC3444f8101754aBC46c52416550D1"

const NEW_PAIR_EVENT = "0x0d3648bd0f6ba80134a33ba9275ac585d9d315f0ad8355cddefde31afa28d0e9"

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

	filterQuery := ethereum.FilterQuery{
		Addresses: []common.Address{pancakeSwapFactoryAddress},
		Topics:    [][]common.Hash{{common.HexToHash(NEW_PAIR_EVENT)}},
	}

	newPairLogChannel := make(chan types.Log)

	log.Println("==> Listening to new pair events...")

	sub, err := client.SubscribeFilterLogs(context.Background(), filterQuery, newPairLogChannel)
	if err != nil {
		log.Fatal(err)
	}

	for {
		select {
		case err := <-sub.Err():
			log.Fatal(err)
		case vLog := <-newPairLogChannel:
			log.Println("==> New pair created")

			log.Println(vLog)
		}
	}
}
