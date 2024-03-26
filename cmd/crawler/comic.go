package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/PuerkitoBio/goquery"
	"github.com/redis/go-redis/v9"
	"log"
	"os"
	"path"
	"path/filepath"
	"sync"
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

func (c *Chapter) Download(comicPath string) error {
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
		redisClient.HSet(ctx, cfg.CurrentComic, c.Title, 1)
	}
	return nil
}

type Comic struct {
	Title    string
	Meta     *Meta
	Cover    string
	Chapters []*Chapter
}

func ParseComic(doc *goquery.Document) *Comic {
	var meta = &Meta{}
	var comic = &Comic{}

	cover, exists := doc.Find(".detail-main-cover > img").Attr("data-original")
	if exists {
		comic.Cover = cover
	}
	comic.Title = doc.Find(".detail-main-info-title").Text()
	cfg.CurrentComic = comic.Title
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
			chapter.Url = cfg.BaseUrl + url
		}
		chapter.Title = s.Text()
		comic.Chapters = append(comic.Chapters, chapter)
	})
	comic.Meta = meta
	return comic
}

func (comic *Comic) Download() error {
	// 新建目录
	dir := path.Join(cfg.DownloadDir, comic.Title)
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
			err = chapter.Download(dir)
			if err != nil {
				log.Printf("Download chapter: %s error: %v", chapter.Title, err)
				return
			}
			log.Println(chapter.Title, "结束抓取")
		}(chapter)
	}
	wg.Wait()
	defer func() {
		if completeNum, _ := redisClient.HLen(ctx, cfg.CurrentComic).Result(); completeNum == int64(len(comic.Chapters)) {
			redisClient.HSet(ctx, "comics", cfg.CurrentComic, 1).Result()
			// zip
			CheckDir(cfg.ArchiveDir)
			err := ZipSource(path.Join(cfg.DownloadDir, comic.Title), path.Join(cfg.ArchiveDir, comic.Title+".zip"))
			if err != nil {
				fmt.Println("zip error: ", err)
				return
			}
		}
	}()
	return nil
}
