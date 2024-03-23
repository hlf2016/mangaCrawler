package main

import (
	"encoding/json"
	"fmt"
	"github.com/PuerkitoBio/goquery"
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

func (c *Chapter) Download(comicPath string) error {
	var wg sync.WaitGroup
	// 限制并发数量
	maxGoroutines := 20
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
	fmt.Println(imgUrls, len(imgUrls))
	for _, imgUrl := range imgUrls {
		wg.Add(1)
		guard <- struct{}{} // 会阻塞，直到有空闲位置
		go func(imgUrl string) {
			defer wg.Done()
			// 完成一个 goroutine 那就释放一个空位置出来
			defer func() { <-guard }()

			imgName := filepath.Base(imgUrl)
			fmt.Println(imgName)
			err = DownloadImage(imgUrl, chapterPath, imgName)
			if err != nil {
				log.Printf("Download chapter image url: %s  error: %v", imgUrl, err)
			}
		}(imgUrl)
	}
	wg.Wait()
	return nil
}

type Comic struct {
	Title    string
	Meta     *Meta
	Cover    string
	Chapters []*Chapter
}

func RequestUrl(url string) (*http.Response, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		log.Printf("Error creating request: %v", err)
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Linux; Android 6.0; Nexus 5 Build/MRA58N) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.0.0 Mobile Safari/537.36")
	client := &http.Client{}

	// 重试 3 次
	for i := 0; i < 5; i++ {
		resp, err := client.Do(req)
		if err != nil {
			log.Printf("Error sending request: %v ", err)
			time.Sleep(time.Duration(2*i) * time.Second) // 指数退避策略
			continue
		}

		if resp.StatusCode != http.StatusOK {
			log.Printf("Server returned non-200 status: %d ", resp.StatusCode)
			time.Sleep(time.Duration(2*i) * time.Second)
			continue
		}

		return resp, nil
	}
	return nil, err
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

func parseComic(doc *goquery.Document) *Comic {
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

func DownloadImage(url string, dir string, filename string) error {
	resp, err := RequestUrl(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	filePath := path.Join(dir, filename)
	out, err := os.Create(filePath)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	if err != nil {
		return err
	}
	return nil
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
	for _, chapter := range comic.Chapters {
		err = chapter.Download(dir)
		if err != nil {
			return err
		}
		os.Exit(1)
	}
	return nil
}

const BaseUrl = "https://www.mxs13.cc"

func main() {
	url := BaseUrl + "/book/620"
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
