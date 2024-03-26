package main

import (
	"archive/zip"
	"crypto/tls"
	"fmt"
	"github.com/PuerkitoBio/goquery"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

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

func CheckDir(path string) error {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		err = os.MkdirAll(path, 0755)
		if err != nil {
			return err
		}
	}
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

	defer resp.Body.Close()

	return nil
}

// ZipSource 将指定的 srcDir 文件夹打包到 destZip 文件中。
func ZipSource(srcDir, destZip string) error {
	zipFile, err := os.Create(destZip)
	if err != nil {
		return err
	}
	defer zipFile.Close()

	archive := zip.NewWriter(zipFile)
	defer archive.Close()

	// Walk 通过文件夹及其子文件夹
	filepath.Walk(srcDir, func(path string, info fs.FileInfo, err error) error {
		fmt.Println(path)
		if err != nil {
			return err
		}
		// 当path和srcDir一样时，说明是根目录本身，不需要写入ZIP中
		if path == srcDir {
			return nil
		}
		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		// 更新header的Name字段，保持结构
		// 保持目录结构的同时，去掉文件路径中的 srcDir 部分
		// 注意这里假设了srcDir不以'/'结尾，如果可能以'/'结尾，需要做适当处理
		header.Name = strings.TrimPrefix(path, srcDir+string(filepath.Separator))
		// 判断文件是否为文件夹
		if info.IsDir() {
			header.Name += "/"
		} else {
			// 设置压缩方法
			header.Method = zip.Deflate
		}
		writer, err := archive.CreateHeader(header)
		if err != nil {
			return err
		}
		if !info.IsDir() {
			file, err := os.Open(path)
			if err != nil {
				return err
			}
			defer file.Close()
			_, err = io.Copy(writer, file)
		}
		return err
	})
	return nil
}
