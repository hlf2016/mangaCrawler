package main

import (
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path"
	"time"
)

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
