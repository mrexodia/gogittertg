package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-telegram-bot-api/telegram-bot-api"
	"github.com/jinzhu/configor"
	"github.com/sromku/go-gitter"
)

type Config struct {
	Gitter struct {
		Token  string `required:"true"`
		RoomId string `required:"true"`
	}
	Telegram struct {
		Token         string `required:"true"`
		Admins        string `required:"true"`
		GroupId       string `default:"0"`
		ImgurClientId string `default:""`
	}
}

type ImgurResponse struct {
	Data    ImageData `json:"data"`
	Status  int       `json:"status"`
	Success bool      `json:"success"`
}

type ImageData struct {
	Account_id int    `json:"account_id"`
	Animated   bool   `json:"animated"`
	Bandwidth  int    `json:"bandwidth"`
	DateTime   int    `json:"datetime"`
	Deletehash string `json:"deletehash"`
	Favorite   bool   `json:"favorite"`
	Height     int    `json:"height"`
	Id         string `json:"id"`
	In_gallery bool   `json:"in_gallery"`
	Is_ad      bool   `json:"is_ad"`
	Link       string `json:"link"`
	Name       string `json:"name"`
	Size       int    `json:"size"`
	Title      string `json:"title"`
	Type       string `json:"type"`
	Views      int    `json:"views"`
	Width      int    `json:"width"`
}

func imgurUploadImageByURL(clientID string, imageURL string) (string, error) {
	req, err := http.NewRequest("POST", "https://api.imgur.com/3/image", strings.NewReader(url.Values{"image": {imageURL}}.Encode()))
	if err != nil {
		return "", err
	}

	req.Header.Add("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Add("Authorization", "Client-ID "+clientID)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()

	var imgurResponse ImgurResponse
	err = json.NewDecoder(res.Body).Decode(&imgurResponse)
	if err != nil {
		return "", err
	}
	if !imgurResponse.Success {
		return "", errors.New("imgur API returned negative response")
	}
	fmt.Println("Image Link: " + imgurResponse.Data.Link)
	fmt.Println("Deletion Link: http://imgur.com/delete/" + imgurResponse.Data.Deletehash)
	return imgurResponse.Data.Link, nil
}

func stringInSlice(a string, list []string) bool {
	for _, b := range list {
		if b == a {
			return true
		}
	}
	return false
}

func gitterEscape(msg string) string {
	// [![asm.png](https://files.gitter.im/x64dbg/x64dbg/0I1c/thumb/asm.png)](https://files.gitter.im/x64dbg/x64dbg/0I1c/asm.png)
	r1 := regexp.MustCompile("^\\[!\\[[^\\]]+\\]\\(https?:\\/\\/files\\.gitter\\.im\\/[^\\/]+\\/[^\\/]+\\/[^\\/]+\\/thumb\\/[^\\)]+\\)\\]\\(([^\\)]+)\\)$")
	msg = r1.ReplaceAllString(msg, "$1")
	// [test.exe](https://files.gitter.im/x64dbg/x64dbg/ROVJ/test.exe)
	r2 := regexp.MustCompile("\\[[^\\]]+\\]\\((https:\\/\\/files\\.gitter\\.im\\/[^\\/]+\\/[^\\/]+\\/[^\\/]+\\/[^\\)]+)\\)$")
	msg = r2.ReplaceAllString(msg, "$1")
	return msg
}

func goGitterIrcTelegram(conf Config) {

	//Gitter init
	api := gitter.New(conf.Gitter.Token)
	user, _ := api.GetUser()
	stream := api.Stream(conf.Gitter.RoomId)
	go api.Listen(stream)
	fmt.Printf("[Gitter] Authorized on user %v\n", user.Username)
	fmt.Printf("[Gitter] RoomId: %v\n", conf.Gitter.RoomId)

	//Telegram init
	bot, err := tgbotapi.NewBotAPI(conf.Telegram.Token)
	if err != nil {
		fmt.Printf("[Telegram] Error in NewBotAPI: %v...\n", err)
		return
	}
	fmt.Printf("[Telegram] Authorized on account %s\n", bot.Self.UserName)
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates, err := bot.GetUpdatesChan(u)
	if err != nil {
		fmt.Printf("[Telegram] Error in GetUpdatesChan: %v...\n", err)
		return
	}
	groupId, err := strconv.ParseInt(conf.Telegram.GroupId, 10, 64)
	if err != nil {
		fmt.Printf("[Telegram] Error parsing GroupId: %v...\n", err)
		groupId = 0
	}
	fmt.Printf("[Telegram] GroupId: %v\n", groupId)

	retries := 0

	//Gitter loop
	go func() {
		for {
			event := <-stream.Event
			switch ev := event.Data.(type) {
			case *gitter.MessageReceived:
				from := ev.Message.From.Username
				if from != user.Username {
					fmt.Printf("[Gitter] <%v> %v\n", from, ev.Message.Text)
					//send to Telegram
					if groupId != 0 {
						bot.Send(tgbotapi.NewMessage(groupId, ev.Message.Text))
					}
				}
			case *gitter.GitterConnectionClosed:
				fmt.Printf("[Gitter] connection was closed (%v)", retries)
				//send to Telegram
				if groupId != 0 {
					bot.Send(tgbotapi.NewMessage(groupId, fmt.Sprintf("[Gitter] connection was closed (%v)", retries)))
				}
				time.Sleep(5 * time.Second)
				retries++
				if retries < 10 {
					stream = api.Stream(conf.Gitter.RoomId)
					go api.Listen(stream)
				}
			}
		}
	}()

	//Telegram loop
	for update := range updates {
		//copy variables
		message := update.Message
		if message == nil {
			fmt.Printf("[Telegram] message == nil\n%v\n", update)
			continue
		}
		chat := message.Chat
		if chat == nil {
			fmt.Printf("[Telegram] chat == nil\n%v\n", update)
			continue
		}
		name := message.From.UserName
		if len(name) == 0 {
			name = message.From.FirstName
		}
		//TODO: use goroutines if it turns out people are sending a lot of photos
		if len(conf.Telegram.ImgurClientId) > 0 && message.Photo != nil && len(*message.Photo) > 0 {
			photo := (*message.Photo)[len(*message.Photo)-1]
			url, err := bot.GetFileDirectURL(photo.FileID)
			if err != nil {
				fmt.Printf("GetFileDirectURL error: %v\n", err)
			} else {
				url, err = imgurUploadImageByURL(conf.Telegram.ImgurClientId, url)
				if err != nil {
					fmt.Printf("imgurUploadImageByURL error: %v\n", err)
				} else {
					if len(message.Caption) > 0 {
						message.Text = fmt.Sprintf("%v %v", message.Caption, url)
					} else {
						message.Text = url
					}
				}
			}
		}
		if len(message.Text) == 0 {
			continue
		}
		//construct/log message
		fmt.Printf("[Telegram] <%v> %v\n", name, message.Text)
		//check for admin commands
		if stringInSlice(message.From.UserName, strings.Split(conf.Telegram.Admins, " ")) && strings.HasPrefix(message.Text, "/") {
			if message.Text == "/start" {
				groupId = chat.ID
			} else if message.Text == "/status" {
				bot.Send(tgbotapi.NewMessage(int64(message.From.ID), fmt.Sprintf("groupId: %v", groupId)))
			}
		} else if groupId != 0 {
			//send to Gitter
			api.SendMessage(conf.Gitter.RoomId, message.Text)
		} else {
			fmt.Println("[Telegam] Use /start to start the bot...")
		}
	}
}

func main() {
	fmt.Println("Gitter/Telegram PM Sync Bot, written in Go by mrexodia")
	var conf Config
	if err := configor.Load(&conf, "config.json"); err != nil {
		fmt.Printf("Error loading config: %v...\n", err)
		return
	}
	goGitterIrcTelegram(conf)
}
