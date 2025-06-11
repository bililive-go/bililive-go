package huya

import (
	"bytes"
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"math/rand"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/bililive-go/bililive-go/src/live"
	"github.com/bililive-go/bililive-go/src/pkg/utils"
	"github.com/hr3lxphr6j/requests"
	"github.com/tidwall/gjson"
)

const uaForLol = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36"

var downloaderHeadersForLol = func() map[string]string {
	headers := getGeneralHeadersForDownloader()
	headers["User-Agent"] = uaForLol
	return headers
}()

func GetInfo_ForLol(l *Live, body string) (info *live.Info, err error) {
	var (
		strFilter = utils.NewStringFilterChain(utils.ParseUnicode, utils.UnescapeHTMLEntity)
		hostName  = strFilter.Do(utils.Match1(`"nick":"([^"]*)"`, body))
		roomName  = strFilter.Do(utils.Match1(`"introduction":"([^"]*)"`, body))
		status    = strFilter.Do(utils.Match1(`"isOn":([^,]*),`, body))
	)

	if hostName == "" || roomName == "" || status == "" {
		return nil, live.ErrInternalError
	}

	info = &live.Info{
		Live:     l,
		HostName: hostName,
		RoomName: roomName,
		Status:   status == "true",
	}
	return info, nil
}

func GetStreamInfos_ForLol(l *Live) (infos []*live.StreamUrlInfo, err error) {
	resp, err := requests.Get(l.Url.String(), requests.UserAgent(uaForLol))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status code: %d", resp.StatusCode)
	}
	body, err := resp.Text()
	if err != nil {
		return nil, err
	}

	tmpStrings := strings.Split(body, `stream: `)
	if len(tmpStrings) < 2 {
		return nil, fmt.Errorf("stream json info not found")
	}
	tmpStreamJsonRawString := strings.Split(tmpStrings[1], `};`)
	if len(tmpStreamJsonRawString) < 1 {
		return nil, fmt.Errorf("stream json info end not found. stream text: %s", tmpStrings[1])
	}
	streamJsonRawString := tmpStreamJsonRawString[0]
	if !gjson.Valid(streamJsonRawString) {
		return nil, fmt.Errorf("streamJsonRawString not valid")
	}
	streamJson := gjson.Parse(streamJsonRawString)
	vMultiStreamInfoJson := streamJson.Get("vMultiStreamInfo").Array()
	if len(vMultiStreamInfoJson) == 0 {
		return nil, fmt.Errorf("vMultiStreamInfo not found")
	}

	streamInfoJsons := streamJson.Get("data.0.gameStreamInfoList").Array()
	if len(streamInfoJsons) == 0 {
		return nil, fmt.Errorf("gameStreamInfoList not found")
	}
	index := l.LastCdnIndex
	if index >= len(streamInfoJsons) {
		index = 0
	}
	l.LastCdnIndex = index + 1
	gameStreamInfoJson := streamInfoJsons[index]
	urls, err := getStreamUrlsFromGameStreamInfoJson(gameStreamInfoJson)
	if err != nil {
		return nil, err
	}
	return utils.GenUrlInfos(urls, downloaderHeadersForLol), nil
}

func getStreamUrlsFromGameStreamInfoJson(gameStreamInfoJson gjson.Result) (us []*url.URL, err error) {
	// get streamName
	sStreamName := gameStreamInfoJson.Get("sStreamName").String()
	// get sFlvAntiCode
	sFlvAntiCode := gameStreamInfoJson.Get("sFlvAntiCode").String()
	// get sFlvUrl
	sFlvUrl := gameStreamInfoJson.Get("sFlvUrl").String()
	// get random uid
	uid := rand.Int63n(99999999999) + 1200000000000

	query, err := parseAntiCode(sFlvAntiCode, uid, sStreamName)
	if err != nil {
		return nil, err
	}
	tmpUrlString := fmt.Sprintf("%s/%s.flv?%s", sFlvUrl, sStreamName, query)
	u, err := url.Parse(tmpUrlString)
	if err != nil {
		return nil, err
	}
	return []*url.URL{u}, nil
}

type urlQueryParams struct {
	WsSecret string
	WsTime   string
	Seqid    string
	Ctype    string
	Ver      string
	Fs       string
	U        string
	T        string
	Sv       string
	Sdk_sid  string
	Codec    string
}

func parseAntiCode(anticode string, uid int64, streamName string) (string, error) {
	qr, err := url.ParseQuery(anticode)
	if err != nil {
		return "", err
	}
	resultTemplate := template.Must(template.New("urlQuery").Parse(
		"wsSecret={{.WsSecret}}" +
			"&wsTime={{.WsTime}}" +
			"&seqid={{.Seqid}}" +
			"&ctype={{.Ctype}}" +
			"&ver={{.Ver}}" +
			"&fs={{.Fs}}" +
			"&u={{.U}}" +
			"&t={{.T}}" +
			"&sv={{.Sv}}" +
			"&sdk_sid={{.Sdk_sid}}" +
			"&codec={{.Codec}}",
	))
	timeNow := time.Now().Unix() * 1000
	resultParams := urlQueryParams{
		WsSecret: "",
		WsTime:   qr.Get("wsTime"),
		Seqid:    strconv.FormatInt(timeNow+uid, 10),
		Ctype:    qr.Get("ctype"),
		Ver:      "1",
		Fs:       qr.Get("fs"),
		U:        strconv.FormatInt(uid, 10),
		T:        "100",
		Sv:       "2405220949",
		Sdk_sid:  strconv.FormatInt(uid, 10),
		Codec:    "264",
	}
	ss := getMD5Hash(fmt.Sprintf("%s|%s|%s", resultParams.Seqid, resultParams.Ctype, resultParams.T))

	decodeString, _ := base64.StdEncoding.DecodeString(qr.Get("fm"))
	fm := string(decodeString)
	fm = strings.ReplaceAll(fm, "$0", resultParams.U)
	fm = strings.ReplaceAll(fm, "$1", streamName)
	fm = strings.ReplaceAll(fm, "$2", ss)
	fm = strings.ReplaceAll(fm, "$3", resultParams.WsTime)

	resultParams.WsSecret = getMD5Hash(fm)
	var buf bytes.Buffer
	if err := resultTemplate.Execute(&buf, resultParams); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func getMD5Hash(text string) string {
	hash := md5.Sum([]byte(text))
	return hex.EncodeToString(hash[:])
}
