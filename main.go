package main

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

type ConConfig struct {
	Broker   string
	Topic    string
	Username string
	Password string
	Cafile   string
	Cert     string
	Key      string
}

var ExitFlag bool = false

type Body struct {
	Token string `json:"token"`
}

func main() {
	host := flag.String("host", "127.0.0.1", "server hostname or IP")
	port := flag.Int("port", 8883, "server port")
	topic := flag.String("topic", "test/abc", "publish/subscribe topic")
	username := flag.String("username", "test", "username")
	password := flag.String("password", "test", "password")
	cafile := flag.String("cafile", "/home/turleft/go/src/emqx-client/ca/ca.pem",
		"path to a file containing trusted CA certificates to enable encryptedommunication.")
	cert := flag.String("cert", "/home/turleft/go/src/emqx-client/ca/client.pem",
		"client certificate for authentication, if required by server.")
	key := flag.String("key", "/home/turleft/go/src/emqx-client/ca/client.key",
		"client certificate for authentication, if required by server.")
	flag.Parse()

	get, err := http.Get("http://localhost:8081/token")
	if err != nil {
		log.Printf("error [%s]\n", err.Error())
	}
	defer func(Body io.ReadCloser) {
		err := Body.Close()
		if err != nil {

		}
	}(get.Body) // 确保在函数退出时关闭响应体

	// 读取响应体内容
	body, err := io.ReadAll(get.Body)
	if err != nil {
		log.Printf("error reading response body: %s\n", err.Error())
		return
	}
	data := Body{}
	err = json.Unmarshal(body, &data)
	if err != nil {
		log.Printf("error reading response body: %s\n", err.Error())
	}
	password = &data.Token

	config := ConConfig{
		Broker:   fmt.Sprintf("tls://%s:%d", *host, *port),
		Topic:    *topic,
		Username: *username,
		Password: *password,
		Cafile:   *cafile,
		Cert:     *cert,
		Key:      *key,
	}
	client := mqttConnect(&config)
	go sub(client, &config)
	//publish(client, &config)

	// 设置信号处理
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)

	// 启动 MQTT 订阅
	go func() {
		for {
			select {
			case sig := <-c:
				fmt.Printf("Received signal: %v\n", sig)
				fmt.Println("Disconnecting from MQTT broker...")
				client.Disconnect(250)
				os.Exit(0)
			}
		}
	}()
	// 阻塞主 goroutine
	select {}
}

func publish(client mqtt.Client, config *ConConfig) {
	for !ExitFlag {
		payload := "The current time " + time.Now().String()
		if client.IsConnectionOpen() {
			token := client.Publish(config.Topic, 0, false, payload)
			if token.Error() != nil {
				log.Printf("pub message to topic %s error:%s \n", config.Topic, token.Error())
			} else {
				log.Printf("pub %s to topic [%s]\n", payload, config.Topic)
			}
		}
		time.Sleep(5 * time.Second)
	}
}

func sub(client mqtt.Client, config *ConConfig) {
	token := client.Subscribe(config.Topic, 0, func(client mqtt.Client, msg mqtt.Message) {
		log.Printf("sub [%s] by [%s] %s \n ", msg.Topic(), strconv.Itoa(int(msg.MessageID())), string(msg.Payload()))
	})
	if token.Error() != nil {
		log.Printf("sub to message error from topic:%s \n", config.Topic)
	}
	ack := token.WaitTimeout(3 * time.Second)
	if !ack {
		log.Printf("sub to topic timeout: %s \n", config.Topic)
	}
}

func SetAutoReconnect(config *ConConfig, opts *mqtt.ClientOptions) {
	firstReconnectDelay, maxReconnectDelay, maxReconnectCount, reconnectRate := 1, 60, 12, 2

	opts.SetOnConnectHandler(func(client mqtt.Client) {
		sub(client, config)
		log.Println("Connected to MQTT Broker!")
	})

	opts.SetConnectionLostHandler(func(client mqtt.Client, err error) {
		fmt.Printf("Connection lost: %v\nTrying to reconnect...\n", err)

		reconnectDelay := firstReconnectDelay
		for i := 0; i < maxReconnectCount; i++ {
			log.Printf("Reconnecting in %ds.\n", reconnectDelay)
			time.Sleep(time.Duration(reconnectDelay) * time.Second)
			if token := client.Connect(); token.Wait() && token.Error() != nil {
				log.Printf("Failed to reconnect: %v\n", token.Error())
			} else if client.IsConnectionOpen() {
				return
			}
			if i != maxReconnectCount-1 {
				log.Println("Reconnect failed, waiting for the next reconnection.")
			}
			reconnectDelay *= reconnectRate
			if reconnectDelay > maxReconnectDelay {
				reconnectDelay = maxReconnectDelay
			}
		}
		log.Printf("Reconnect failed after %d attempts. Exiting...", maxReconnectCount)
		ExitFlag = !client.IsConnectionOpen()
	})
}

func mqttConnect(config *ConConfig) mqtt.Client {
	opts := mqtt.NewClientOptions()
	opts.AddBroker(config.Broker)
	opts.SetUsername(config.Username)
	opts.SetPassword(config.Password)
	opts.TLSConfig = loadTLSConfig(config)
	opts.SetKeepAlive(3 * time.Second)
	SetAutoReconnect(config, opts)
	client := mqtt.NewClient(opts)
	token := client.Connect()
	ack := token.WaitTimeout(5 * time.Second)
	if token.Error() != nil || !ack {
		log.Fatalf("connect%s mqtt server error: %s", config.Broker, token.Error())
	}
	return client
}

func loadTLSConfig(config *ConConfig) *tls.Config {
	// load tls config

	var tlsConfig tls.Config
	tlsConfig.InsecureSkipVerify = false
	if config.Cafile != "" {
		certpool := x509.NewCertPool()
		ca, err := ioutil.ReadFile(config.Cafile)
		if err != nil {
			log.Fatalln(err.Error())
		}
		certpool.AppendCertsFromPEM(ca)
		tlsConfig.RootCAs = certpool
	}
	if config.Cert != "" && config.Key != "" {
		clientKeyPair, err := tls.LoadX509KeyPair(config.Cert, config.Key)
		if err != nil {
			log.Fatalln(err.Error())
		}
		tlsConfig.ClientAuth = tls.RequestClientCert
		tlsConfig.Certificates = []tls.Certificate{clientKeyPair}
	}
	return &tlsConfig
}
