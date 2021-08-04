package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/ndphu/message-handler-lib/service"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/signal"
	"rpc-proxy/proxy"
	"syscall"
)

func main() {

	fmt.Println("HTTP_PROXY", os.Getenv("HTTP_PROXY"))
	fmt.Println("HTTPS_PROXY", os.Getenv("HTTPS_PROXY"))

	startWeb()
	startRmq()
}

func startRmq() {
	serviceId := os.Getenv("SERVICE_ID")
	if serviceId == "" {
		if s, err := ioutil.ReadFile(".service_id"); err != nil {
			serviceId = uuid.New().String()
			if err := ioutil.WriteFile(".service_id", []byte(serviceId), 0755); err != nil {
				panic(err)
			}
		} else {
			serviceId = string(s)
		}
	}

	s := service.NewService(serviceId,
		service.Description{
			Name:    "raspberry-proxy-service",
			Type:    "proxy-service",
			Version: "0.0.1",
		},
		[]service.Action{
			{
				Name:          "proxy:request",
				ArgumentCount: 1,
				Handler: func(args []string) (interface{}, error) {
					decoded, err := base64.StdEncoding.DecodeString(args[0])
					if err != nil {
						return nil, err
					}
					var pr proxy.Request
					if err := json.Unmarshal(decoded, &pr); err != nil {
						log.Println("Fail to unmarshal first argument to ProxyRequest object")
						return nil, err
					}

					return handleProxyRequest(pr)
				},
			},
		})
	if err := s.Start(); err != nil {
		panic(err)
	}

	termChan := make(chan os.Signal)
	signal.Notify(termChan, syscall.SIGINT, syscall.SIGTERM)
	<-termChan
	log.Println("Shutdown signal received")
	_ = s.Stop()
}

func startWeb() {
	r := gin.Default()

	c := cors.DefaultConfig()
	c.AllowAllOrigins = true
	c.AllowCredentials = true
	c.AllowMethods = []string{"GET", "POST", "PUT", "PATCH", "DELETE"}
	c.AllowHeaders = []string{"Origin", "Authorization", "Content-Type", "Content-Length", "X-Requested-With"}

	r.Use(cors.New(c))

	r.POST("/api/proxy", func(c *gin.Context) {
		var pr proxy.Request
		if err := c.ShouldBindJSON(&pr); err != nil {
			c.AbortWithStatusJSON(400, gin.H{"success": false, "error": err.Error()})
			return
		}

		response, err := handleProxyRequest(pr)
		if err != nil {
			c.AbortWithStatusJSON(500, gin.H{"success": false, "error": err.Error()})
			return
		}
		c.JSON(200, response)
	})

	go r.Run()
}

func handleProxyRequest(pr proxy.Request) (*proxy.Response, error) {
	// prepare request body
	var body = bytes.NewReader(make([]byte, 0))
	if len(pr.Body) > 0 {
		decoded, err := base64.StdEncoding.DecodeString(pr.Body)
		if err != nil {
			log.Println("Proxy", "Fail to parse body by error", err.Error())
			return nil, err
		}
		if len(decoded) > 0 {
			body = bytes.NewReader(decoded)
		}
	}

	// make request to target website
	req, err := http.NewRequest(pr.Method, pr.Url, body)
	if err != nil {
		log.Println("Proxy", "Fail to create request by error", err.Error())
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/92.0.4515.131 Safari/537.36")
	for header, values := range pr.Headers {
		for _, value := range values {
			fmt.Printf(`Adding header %s = "%s"\n`, header, value)
			req.Header.Add(header, value)
		}
	}
	log.Println("PROXY:DEBUG", "HTTP_PROXY", os.Getenv("HTTP_PROXY"))
	log.Println("PROXY:DEBUG", "HTTPS_PROXY", os.Getenv("HTTPS_PROXY"))
	log.Println("PROXY: Making real request to:", pr.Url)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Println("Proxy", "Fail to parse make http request by error", err.Error())
		return nil, err
	}
	log.Println("PROXY: Finished making request to:", pr.Url)
	defer resp.Body.Close()
	all, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Println("Fail to read response body by error", err.Error())
		return nil, err
	}
	response := proxy.Response{
		Success:    true,
		Error:      "",
		StatusCode: resp.StatusCode,
		Headers:    resp.Header,
		Body:       base64.StdEncoding.EncodeToString(all),
	}
	return &response, nil
}
