package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/redis/go-redis/v9"
	"log"
	"strconv"
)

func initRedis() {
	redisClient = redis.NewClient(&redis.Options{
		Addr:     cfg.Redis.Addr,
		Password: "", // no password set
		DB:       0,  // use default DB
	})
	_, err := redisClient.Ping(ctx).Result()
	if err != nil {
		panic(err)
	}
}

var (
	redisClient *redis.Client
	ctx         = context.Background()
)

type config struct {
	BaseUrl      string
	CurrentComic string
	CurrentID    int
	DownloadDir  string
	ArchiveDir   string
	Redis        struct {
		Addr     string
		Password string
		DB       int
	}
}

var cfg config

func main() {
	flag.IntVar(&cfg.CurrentID, "id", 499, "漫画 ID")
	flag.StringVar(&cfg.Redis.Addr, "redis_addr", "localhost:6379", "用到的 redis 地址 ip:port 格式")
	flag.StringVar(&cfg.BaseUrl, "base_url", "https://www.mxs13.cc", "网址 默认是 https://www.mxs13.cc")
	flag.StringVar(&cfg.DownloadDir, "download_dir", "./comics", "本地下载存放路径 默认 ./comics")
	flag.StringVar(&cfg.ArchiveDir, "archive_dir", "./archives", "本地下载存放路径 默认 ./archives")
	flag.Parse()

	initRedis()
	url := cfg.BaseUrl + "/book/" + strconv.Itoa(cfg.CurrentID)
	fmt.Println(cfg.CurrentID, url)
	doc, err := Resp2Doc(url)
	if err != nil {
		log.Printf("Response to doc error: %v", err)
		return
	}
	comic := ParseComic(doc)
	// fmt.Println(comic)
	err = comic.Download()
	if err != nil {
		log.Printf("Comic download error: %v", err)
		return
	}
}
