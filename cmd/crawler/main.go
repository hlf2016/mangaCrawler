package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/PuerkitoBio/goquery"
	"github.com/redis/go-redis/v9"
	"io"
	"log"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sync"
	"time"
)

type Meta struct {
	Author     string   `json:"author"`
	Area       string   `json:"area"`
	AliasTitle string   `json:"alias_title"`
	Tags       []string `json:"tags"`
	Desc       string   `json:"desc"`
}

func (m Meta) String() string {
	return fmt.Sprintf("{Author: %s, Area: %s, AliasTitle: %s, Tags: %s, Desc: %s}", m.Author, m.Area, m.AliasTitle, m.Tags, m.Desc)
}

type Chapter struct {
	Url   string
	Title string
}

func (c *Chapter) String() string {
	return fmt.Sprintf("{Url: %s , Title: %s}", c.Url, c.Title)
}

func (c *Chapter) Parse(doc *goquery.Document) []string {
	return doc.Find("#cp_img img").Map(func(i int, s *goquery.Selection) string {
		imgUrl, _ := s.Attr("data-original")
		return imgUrl
	})
}

func (c *Chapter) Download(comicPath string, comicTitle string) error {
	var wg sync.WaitGroup
	// 限制并发数量
	maxGoroutines := 5
	// 用 struct{} 作为信号类型的原因  不占任何内存
	guard := make(chan struct{}, maxGoroutines)

	chapterPath := path.Join(comicPath, c.Title)
	err := CheckDir(chapterPath)
	if err != nil {
		return err
	}

	doc, err := Resp2Doc(c.Url)
	if err != nil {
		return err
	}
	imgUrls := c.Parse(doc)
	fmt.Println(len(imgUrls))
	for i, imgUrl := range imgUrls {
		offset := int64(i)
		doneImg, err := redisClient.GetBit(ctx, c.Title, offset).Result()
		if err != nil && !errors.Is(err, redis.Nil) {
			return err
		}
		if doneImg == 1 {
			continue
		}
		wg.Add(1)
		guard <- struct{}{} // 会阻塞，直到有空闲位置
		go func(imgUrl string, offset int64) {
			defer wg.Done()
			// 完成一个 goroutine 那就释放一个空位置出来
			defer func() { <-guard }()

			imgName := filepath.Base(imgUrl)
			fmt.Println(imgName)
			err = DownloadImage(imgUrl, chapterPath, imgName)
			if err != nil {
				log.Printf("Download chapter image url: %s  error: %v", imgUrl, err)
				return
			}
			// 抓取成功 做标记
			redisClient.SetBit(ctx, c.Title, offset, 1)
		}(imgUrl, offset)
	}
	wg.Wait()
	// 推出前 判断整章是否都抓取完毕 设置标志
	completeNum, err := redisClient.BitCount(ctx, c.Title, nil).Result()
	if err != nil {
		return err
	}
	if completeNum == int64(len(imgUrls)) {
		// 已经抓取完毕
		redisClient.HSet(ctx, comicTitle, c.Title, 1)
	}
	return nil
}

type Comic struct {
	Title    string
	Meta     *Meta
	Cover    string
	Chapters []*Chapter
}

func Resp2Doc(url string) (*goquery.Document, error) {
	resp, err := RequestUrl(url)
	if err != nil {
		log.Printf("Rquest url error: %v", err)
		return nil, err
	}
	defer resp.Body.Close()
	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		log.Printf("goquery new document error: %v", err)
		return nil, err
	}
	return doc, nil
}

func ParseComic(doc *goquery.Document) *Comic {
	var meta = &Meta{}
	var comic = &Comic{}

	cover, exists := doc.Find(".detail-main-cover > img").Attr("data-original")
	if exists {
		comic.Cover = cover
	}
	comic.Title = doc.Find(".detail-main-info-title").Text()
	meta.AliasTitle = doc.Find(".detail-main-info-author > a").Eq(0).Text()
	meta.Author = doc.Find(".detail-main-info-author > a").Eq(1).Text()
	meta.Area = doc.Find(".detail-main-info-author > a").Eq(2).Text()
	meta.Tags = doc.Find(".detail-main-info-class a").Map(func(i int, s *goquery.Selection) string {
		return s.Text()
	})
	meta.Desc = doc.Find(".detail-desc").Text()
	doc.Find("#detail-list-select .chapteritem").Each(func(i int, s *goquery.Selection) {
		chapter := &Chapter{}
		url, exists := s.Attr("href")
		if exists {
			chapter.Url = BaseUrl + url
		}
		chapter.Title = s.Text()
		comic.Chapters = append(comic.Chapters, chapter)
	})
	comic.Meta = meta
	return comic
}

func CheckDir(path string) error {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		err = os.MkdirAll(path, 0755)
		if err != nil {
			return err
		}
	}
	return nil
}

func (comic *Comic) Download() error {
	// 新建目录
	dir := "./comics/" + comic.Title
	err := CheckDir(dir)
	if err != nil {
		return err
	}
	// 下载 cover
	err = DownloadImage(comic.Cover, dir, "cover.jpg")
	if err != nil {
		log.Printf("Save image error: %v", err)
		return err
	}
	// 下载 meta json 信息
	metaJson, err := json.MarshalIndent(comic.Meta, "", "  ") // 更加美观的 json 结构
	if err != nil {
		return err
	}
	jsonFile, err := os.Create(path.Join(dir, "meta.json"))
	if err != nil {
		return err
	}
	defer jsonFile.Close()
	_, err = jsonFile.Write(metaJson)
	if err != nil {
		return err
	}
	// fmt.Println(string(metaJson))

	// 下载各章节信息
	var wg sync.WaitGroup
	maxGoroutines := 5
	guard := make(chan struct{}, maxGoroutines)

	for _, chapter := range comic.Chapters {
		existed, err := redisClient.HGet(ctx, comic.Title, chapter.Title).Result()
		// 返回 redis.Nil 表示 键不存在
		if err != nil && !errors.Is(err, redis.Nil) {
			return err
		}
		if existed == "1" {
			fmt.Println(chapter.Title, "已存在，跳过")
			continue
		}
		wg.Add(1)
		guard <- struct{}{}

		go func(chapter *Chapter) {
			defer wg.Done()
			defer func() { <-guard }()
			log.Println(chapter.Title, "开始抓取")
			err = chapter.Download(dir, comic.Title)
			if err != nil {
				log.Printf("Download chapter: %s error: %v", chapter.Title, err)
				return
			}
			log.Println(chapter.Title, "结束抓取")
		}(chapter)
	}
	wg.Wait()
	return nil
}

func RequestUrl(url string) (*http.Response, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		log.Printf("Error creating request: %v", err)
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Linux; Android 6.0; Nexus 5 Build/MRA58N) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.0.0 Mobile Safari/537.36")
	client := &http.Client{
		Transport: &http.Transport{
			// 禁用HTTP/2支持
			TLSNextProto: make(map[string]func(authority string, c *tls.Conn) http.RoundTripper),
		},
	}

	// 重试 5 次
	for i := 0; i < 5; i++ {
		resp, err := client.Do(req)
		if err == nil && resp.StatusCode == http.StatusOK {
			return resp, nil
		}
		// 响应不为 nil 关闭 response
		if resp != nil {
			resp.Body.Close()
		}

		log.Printf("RequestUrl error: %v. Retrying in %v seconds...", err, 2<<uint(i))
		time.Sleep(time.Duration(2<<uint(i)) * time.Second) // 指数退避策略
	}
	return nil, err
}

func DownloadImage(url string, dir string, filename string) error {
	resp, err := RequestUrl(url)
	if err != nil {
		fmt.Println("Download img error", 1)
		return err
	}

	filePath := path.Join(dir, filename)
	out, err := os.Create(filePath)
	if err != nil {
		fmt.Println("Download img error", 2)
		return err
	}
	defer out.Close()

	if _, err = io.Copy(out, resp.Body); err != nil {
		fmt.Println("Download img error", 3)
		return err
	}
	defer func() {
		if resp != nil {
			resp.Body.Close()
		}
	}()
	return nil
}

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
	comic := ParseComic(doc)
	// fmt.Println(comic)
	err = comic.Download()
	if err != nil {
		log.Printf("Comic download error: %v", err)
		return
	}
}
