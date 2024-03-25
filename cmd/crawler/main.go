package main

import (
	"context"
	"github.com/redis/go-redis/v9"
	"log"
)

func initRedis() {
	redisClient = redis.NewClient(&redis.Options{
		Addr:     "localhost:6379",
		Password: "", // no password set
		DB:       0,  // use default DB
	})
	_, err := redisClient.Ping(ctx).Result()
	if err != nil {
		panic(err)
	}
}

var (
	BaseUrl           = "https://www.mxs13.cc"
	redisClient       *redis.Client
	ctx               = context.Background()
	currentComicTitle = ""
)

func main() {
	initRedis()
	url := BaseUrl + "/book/499"
	doc, err := Resp2Doc(url)
	if err != nil {
		log.Printf("Response to doc error: %v", err)
		return
	}
	comic := parseComic(doc)
	// fmt.Println(comic)
	err = comic.Download()
	if err != nil {
		log.Printf("Comic download error: %v", err)
		return
	}
}
