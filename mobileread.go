package main

import (
	"crypto/md5"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

// MobileRead accesses the MobileRead forums.
type MobileRead struct {
	c    *http.Client
	u, p string
}

// NewMobileRead creates a new client and logs in.
func NewMobileRead(c *http.Client, username, password string) (*MobileRead, error) {
	mr := &MobileRead{c, username, password}
	if c.Jar == nil {
		return nil, fmt.Errorf("http client does not have a cookie jar")
	}
	if err := mr.login(false, false, true); err != nil {
		return nil, fmt.Errorf("log in: %w", err)
	}
	return mr, nil
}

func (mr *MobileRead) GetUsername() string {
	return mr.u
}

// Login ensures the user is logged in.
func (mr *MobileRead) Login() error {
	return mr.login(false, false, false)
}

func (mr *MobileRead) NewThread(forum int, subject, message, tagList string, signature, parseURL, disableSmilies bool) (int, error) {
	if err := mr.Login(); err != nil {
		return 0, fmt.Errorf("log in: %w", err)
	}

	resp, err := mr.c.Get("https://www.mobileread.com/forums/newthread.php?do=newthread&f=" + strconv.Itoa(forum))
	if err != nil {
		return 0, fmt.Errorf("get new thread page: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("get new thread page: response status %s", resp.Status)
	}

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("parse new thread page: %w", err)
	}

	form := doc.Find(`form[action*="newthread.php?do=postthread"]`).First()
	if form.Length() == 0 {
		return 0, fmt.Errorf("parse new thread page: could not find post thread form")
	}

	action, err := url.Parse(form.AttrOr("action", ""))
	if err != nil {
		return 0, fmt.Errorf("parse new thread page: parse form action url: %w", err)
	}

	action = resp.Request.URL.ResolveReference(action)

	var fS, fM, fTL, fSi, fPU, fDS, fSu bool
	body := url.Values{}
	form.Find("input[name], textarea[name]").EachWithBreak(func(_ int, s *goquery.Selection) bool {
		t, k, v := s.AttrOr("type", ""), s.AttrOr("name", ""), s.AttrOr("value", "")
		switch t {
		case "checkbox":
			_, cv := s.Attr("checked")
			switch k {
			case "signature":
				cv = signature
				fSi = true
			case "parseurl":
				cv = parseURL
				fPU = true
			case "disablesmilies":
				cv = disableSmilies
				fDS = true
			case "wysiwyg":
				cv = false
			case "postpoll":
				cv = false
			}
			if !cv {
				return true
			}
		case "radio":
			_, rv := s.Attr("checked")
			switch k {
			case "iconid":
				rv = v == "0"
			}
			if !rv {
				return true
			}
			if ev, ok := body[k]; ok {
				err = fmt.Errorf("radio button %q already set to %q", k, ev)
				return false
			}
		case "button", "submit", "clear":
			if k != "sbutton" {
				fSu = true
				return true
			}
		case "hidden", "text", "":
			switch k {
			case "subject":
				if subject == "" {
					err = fmt.Errorf("subject must not be blank")
					return false
				}
				v, fS = subject, true
			case "message":
				if message == "" {
					err = fmt.Errorf("message must not be blank")
					return false
				}
				v, fM = message, true
			case "taglist":
				v, fTL = tagList, true
			}
			// TODO: select?
		}
		body.Set(k, v)
		return true
	})
	if err != nil {
		return 0, err
	}
	if !fS || !fM || !fTL || !fSi || !fPU || !fDS || !fSu {
		return 0, fmt.Errorf("could not find a form field (subject=%t, message=%t, taglist=%t, signature=%t, parseurl=%t, disablesmilies=%t, sbutton=%t)", fS, fM, fTL, fSi, fPU, fDS, fSu)
	}

	tresp, err := mr.c.PostForm(action.String(), body)
	if err != nil {
		return 0, fmt.Errorf("submit post thread form to %q with form body %q: %w", action, body.Encode(), err)
	}
	defer tresp.Body.Close()

	if tresp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("submit post thread form: response status %s", tresp.Status)
	}

	tdoc, err := goquery.NewDocumentFromReader(tresp.Body)
	if err != nil {
		return 0, fmt.Errorf("parse thread page: %w", err)
	}

	h, _ := tdoc.Html()
	fmt.Println(h, tresp.Request.URL)

	if strings.Contains(tresp.Request.URL.Path, "newthread.php") {
		return 0, fmt.Errorf("unknown error posting thread")
	}

	tid := tdoc.Find(`form[action*="threadrate.php"] input[name="t"]`).First()
	if tid.Length() == 0 {
		return 0, fmt.Errorf("parse thread page: could not find thread ID")
	}

	t, err := strconv.Atoi(tid.AttrOr("value", ""))
	if err != nil {
		return 0, fmt.Errorf("parse thread page: could not parse thread ID %q", t)
	}

	return t, nil
}

// login ensures the user is logged in. If checkLogin is true, an error will be
// returned if the user is not currently logged in. Otherwise, if forceLogin is
// true, the login is forced (but may fail due to the form not appearing).
// Otherwise, if expectLogin is true, an error is returned if the user is
// already logged in.
func (mr *MobileRead) login(checkLogin, forceLogin, expectLogin bool) error {
	resp, err := mr.c.Get("https://www.mobileread.com/forums/usercp.php")
	if err != nil {
		return fmt.Errorf("get login page: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("get login page: response status %s", resp.Status)
	}

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return fmt.Errorf("parse login page: %w", err)
	}

	if checkLogin || !forceLogin {
		s := doc.Find(`[name="securitytoken"]`).First()
		if s.Length() == 0 {
			return fmt.Errorf("parse login page: could not find security token")
		}
		switch s.AttrOr("value", "") {
		case "":
			return fmt.Errorf("parse login page: empty security token")
		case "guest":
			if checkLogin {
				return fmt.Errorf("parse login page: user is not logged in")
			}
			break
		default:
			if checkLogin {
				return nil
			}
			if expectLogin {
				return fmt.Errorf("parse login page: expected logged out user, but got security token %q for logged in user", s.AttrOr("value", ""))
			}
			return nil
		}
	}

	form := doc.Find(`form[action*="login.php?do=login"]`).First()
	if form.Length() == 0 {
		return fmt.Errorf("parse login page: could not find login form")
	}

	action, err := url.Parse(form.AttrOr("action", ""))
	if err != nil {
		return fmt.Errorf("parse login page: parse form action url: %w", err)
	}

	action = resp.Request.URL.ResolveReference(action)

	body := url.Values{}
	form.Find("input[name], textarea[name]").EachWithBreak(func(_ int, s *goquery.Selection) bool {
		k, v := s.AttrOr("name", ""), s.AttrOr("value", "")
		switch k {
		case "vb_login_username":
			v = mr.u
		case "vb_login_password":
			if strings.Contains(form.AttrOr("onsubmit", ""), ", 0)") {
				v = ""
			} else {
				v = mr.p
			}
		case "vb_login_md5password":
			for _, c := range mr.p {
				if c > 255 {
					err = fmt.Errorf("generate login url: support for password character codes >255 not implemented (would need to implement entity encoding like str_to_ent to encode the vb_login_md5password param)")
					return false
				}
			}
			fallthrough
		case "vb_login_md5password_utf":
			v = fmt.Sprintf("%x", md5.Sum([]byte(mr.p)))
		}
		body.Set(k, v)
		return true
	})
	if err != nil {
		return err
	}

	var bl []string
	for k := range body {
		bl = append(bl, k)
	}

	lresp, err := mr.c.PostForm(action.String(), body)
	if err != nil {
		return fmt.Errorf("submit login form to %q with form body %q: %w", action, bl, err)
	}
	defer lresp.Body.Close()

	if lresp.StatusCode != http.StatusOK {
		return fmt.Errorf("submit login form: response status %s", lresp.Status)
	}

	ldoc, err := goquery.NewDocumentFromReader(lresp.Body)
	if err != nil {
		return fmt.Errorf("parse login response: %w", err)
	}

	if s := ldoc.Find(`a[href]:contains('redirect')`).First(); s.Length() != 0 {
		href := s.AttrOr("href", "")
		tresp, err := mr.c.Get(href)
		if err != nil {
			return fmt.Errorf("get %q: %w", href, err)
		}
		defer tresp.Body.Close()
		if tresp.StatusCode != http.StatusOK {
			return fmt.Errorf("get %q: response status %s", href, tresp.Status)
		}
	}

	if err := mr.login(true, false, false); err != nil {
		return fmt.Errorf("bad username or password (or another error when logging in) (%v)", err)
	}
	return nil
}
