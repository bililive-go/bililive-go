package weibolive

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/bililive-go/bililive-go/src/pkg/utils"
	"github.com/hr3lxphr6j/requests"
	"github.com/tidwall/gjson"

	"github.com/bililive-go/bililive-go/src/live"
	"github.com/bililive-go/bililive-go/src/live/internal"
)

const (
	domain = "weibo.com"
	cnName = "微博直播"

	liveurl = "https://weibo.com/l/!/2/wblive/room/show_pc_live.json?live_id="
)

func init() {
	live.Register(domain, new(builder))
}

type builder struct{}

func (b *builder) Build(url *url.URL) (live.Live, error) {
	return &Live{
		BaseLive: internal.NewBaseLive(url),
	}, nil
}

type Live struct {
	internal.BaseLive
	roomID string
	userID string
	isUserProfile bool
}

func (l *Live) parseUrl() error {
	paths := strings.Split(l.Url.Path, "/")
	
	// Check if it's a live room URL (has at least 5 path segments)
	if len(paths) >= 5 && paths[1] == "l" && paths[2] == "wblive" {
		// This is a direct live room URL like: /l/wblive/p/show/{room_id}
		if len(paths) < 6 {
			return live.ErrRoomUrlIncorrect
		}
		l.roomID = paths[5]
		l.isUserProfile = false
		return nil
	}
	
	// Check if it's a user profile URL
	if len(paths) >= 3 && paths[1] == "u" {
		// URL format: /u/{user_id}
		if paths[2] == "" {
			return live.ErrRoomUrlIncorrect
		}
		l.userID = paths[2]
		l.isUserProfile = true
		return nil
	} else if len(paths) >= 2 && paths[1] != "" {
		// URL format: /{user_id} (short format)
		// Make sure it's not some other path like /login, /help, etc.
		userID := paths[1]
		// Basic validation: user ID should be numeric
		if regexp.MustCompile(`^[0-9]+$`).MatchString(userID) {
			l.userID = userID
			l.isUserProfile = true
			return nil
		}
	}
	
	return live.ErrRoomUrlIncorrect
}

func (l *Live) getLiveRoomFromUser() error {
	if l.userID == "" {
		return live.ErrRoomUrlIncorrect
	}
	
	// Get the user's profile page to look for live room information
	profileUrl := fmt.Sprintf("https://weibo.com/%s", l.userID)
	resp, err := l.RequestSession.Get(profileUrl, 
		live.CommonUserAgent,
		requests.Headers(map[string]any{
			"Accept": "text/html,application/xhtml+xml,application/xml;q=0.9,image/webp,*/*;q=0.8",
			"Accept-Language": "zh-CN,zh;q=0.8,en-US;q=0.5,en;q=0.3",
		}))
	if err != nil {
		return err
	}
	
	if resp.StatusCode != http.StatusOK {
		return live.ErrRoomNotExist
	}
	
	body, err := resp.Text()
	if err != nil {
		return err
	}
	
	// Look for live room links in the profile page
	// Pattern for live room URLs in the HTML content
	liveRoomRegex := regexp.MustCompile(`https://weibo\.com/l/wblive/p/show/([^"'\s]+)`)
	matches := liveRoomRegex.FindStringSubmatch(body)
	
	if len(matches) < 2 {
		// Also try alternative patterns
		// Sometimes the live room ID appears in different formats
		liveIdRegex := regexp.MustCompile(`"live_id":"([^"]+)"`)
		matches = liveIdRegex.FindStringSubmatch(body)
		
		if len(matches) < 2 {
			return live.ErrRoomNotExist
		}
	}
	
	l.roomID = matches[1]
	return nil
}

func (l *Live) getRoomInfo() ([]byte, error) {
	// Parse the URL to determine if it's a user profile or live room URL
	if err := l.parseUrl(); err != nil {
		return nil, err
	}
	
	// If it's a user profile URL, get the live room ID first
	if l.isUserProfile {
		if err := l.getLiveRoomFromUser(); err != nil {
			return nil, err
		}
	}
	
	if l.roomID == "" {
		return nil, live.ErrRoomUrlIncorrect
	}

	resp, err := l.RequestSession.Get(liveurl+l.roomID,
		live.CommonUserAgent,
		requests.Headers(map[string]any{
			"Referer": l.Url,
		}))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, live.ErrRoomNotExist
	}
	body, err := resp.Bytes()
	if err != nil || gjson.GetBytes(body, "error_code").Int() != 0 {
		return nil, live.ErrRoomNotExist
	}
	return body, nil
}

func (l *Live) GetInfo() (info *live.Info, err error) {
	body, err := l.getRoomInfo()
	if err != nil {
		return nil, live.ErrRoomNotExist
	}
	info = &live.Info{
		Live:         l,
		HostName:     gjson.GetBytes(body, "data.user.screenName").String(),
		RoomName:     gjson.GetBytes(body, "data.title").String(),
		Status:       gjson.GetBytes(body, "data.status").String() == "1",
		CustomLiveId: "weibolive/" + l.roomID,
	}
	return info, nil
}

func (l *Live) GetStreamUrls() (us []*url.URL, err error) {
	body, err := l.getRoomInfo()
	if err != nil {
		return nil, live.ErrRoomNotExist
	}

	streamurl := gjson.GetBytes(body, "data.live_origin_flv_url").String()
	queryParams := l.Url.Query()
	quality := queryParams.Get("q")
	if quality != "" {
		targetQuality := "_wb" + quality + "avc.flv"
		reg, err := regexp.Compile(`_wb[\d]+avc\.flv`)
		if err == nil && reg.MatchString(streamurl) {
			streamurl = reg.ReplaceAllString(streamurl, targetQuality)
		} else {
			streamurl = strings.ReplaceAll(streamurl, ".flv", targetQuality)
		}
		l.GetLogger().Info("weibo stream quality fixed: " + streamurl)
	}

	return utils.GenUrls(streamurl)
}

func (l *Live) GetPlatformCNName() string {
	return cnName
}
