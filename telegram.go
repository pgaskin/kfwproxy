package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

type Telegram struct {
	c *http.Client
	t string
	u string
}

func NewTelegram(c *http.Client, token string) (*Telegram, error) {
	tc := &Telegram{c: c, t: token}
	if tc.c == nil {
		tc.c = http.DefaultClient
	}
	var obj struct {
		Username string `json:"username"`
	}
	if err := tc.api("getMe", nil, &obj); err != nil {
		return nil, err
	} else {
		tc.u = obj.Username
	}
	return tc, nil
}

func (tc *Telegram) GetUsername() string {
	return tc.u
}

func (tc *Telegram) GetChatUsername(id string) (string, error) {
	var obj struct {
		Username string `json:"username"`
	}
	if err := tc.api("getChat", url.Values{
		"chat_id": {id},
	}, &obj); err != nil {
		return "", fmt.Errorf("get chat %#v: %w", id, err)
	}
	return obj.Username, nil
}

func (tc *Telegram) SendMessage(id, text string) error {
	if err := tc.api("sendMessage", url.Values{
		"chat_id":                  {id},
		"text":                     {text},
		"parse_mode":               {"HTML"},
		"disable_web_page_preview": {"true"},
	}, nil); err != nil {
		return fmt.Errorf("send message to %#v: %w", id, err)
	}
	return nil
}

func (tc *Telegram) api(method string, params url.Values, out interface{}) error {
	var p string
	if params != nil {
		p = "?" + params.Encode()
	}

	req, err := http.NewRequest("GET", "https://api.telegram.org/bot"+tc.t+"/"+method+p, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("User-Agent", "kfwproxy (github.com/geek1011/kfwproxy)")

	resp, err := tc.c.Do(req)
	if err != nil {
		return fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	var obj struct {
		OK          bool            `json:"ok"`
		ErrorCode   int             `json:"error_code"`
		Description string          `json:"description"`
		Result      json.RawMessage `json:"result"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&obj); err != nil {
		return fmt.Errorf("read response json: %w", err)
	} else if !obj.OK {
		return fmt.Errorf("api error: %s: %s (%d)", method, obj.Description, obj.ErrorCode)
	}

	if out != nil {
		if err = json.Unmarshal(obj.Result, &out); err != nil {
			return fmt.Errorf("parse result json: %w", err)
		}
	}
	return nil
}
