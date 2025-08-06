package hongdoufm

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"net/http"
	"net/url"
	"strings"

	"github.com/hr3lxphr6j/requests"
	"github.com/tidwall/gjson"

	"github.com/bililive-go/bililive-go/src/live"
	"github.com/bililive-go/bililive-go/src/live/internal"
	"github.com/bililive-go/bililive-go/src/pkg/utils"
)

const (
	domain    = "www.hongdoufm.com"
	domain1   = "live.kilakila.cn"
	cnName    = "克拉克拉"
	secretkey = "c98be79a4347bc97" //密钥
	iv        = "93x0ue23c2c9h8km" //偏移量

	roomInitUrl = "https://live.hongdoulive.com/LiveRoom/getRoomInfo?roomId="
)

func init() {
	live.Register(domain, new(builder))
	live.Register(domain1, new(builder))
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
}

// 克拉克拉平台直播间连接有两种格式
// 1、https://www.hongdoufm.com/room/roomid 这是直播间列表中的房间地址
// 2、http://www.hongdoufm.com/PcLive/index/detail?id=roomid 这是实际直播间地址，上述地址会经过302跳转
// 3、https://www.hongdoufm.com/room/T8T5c0UGXrpW97cbsVP1Qnr5keKGA07wvyC6hdebJXGnGxP91VOt_nFQNhDdKna1SFfDdDECfH46UeFIyPfW3Q==
//
//	room后的路径需要aes解密才能得到2081356094101258743?sign=64840db0d2bfc7f8d23c224a898cefa4问号前面的是直播间id
//
// 4、https://www.hongdoufm.com/PcLive/index/detail?_specific_parameter=W3-Fy88x1VpVp8SIOS_9_Jwt52fAj7OCm07pIritnjzILTCoJjkQLLp7zsPU60cJtrzVgdwx66LiAu0-7IggJQ%3D%3D
// 由3的链接302跳转得到 经3的方式解密得到 id=2081356094101258743&sign=b217a4dcfe2d2b188b0f72196e7636de
func (l *Live) getRoomInfo() ([]byte, error) {
	if strings.Contains(l.Url.String(), "?") {
		//实际直播间地址
		result, _ := url.ParseQuery(l.Url.RawQuery)
		if result.Get("_specific_parameter") != "" {
			urlparam := result.Get("_specific_parameter")
			unescapestr, _ := url.QueryUnescape(urlparam)
			decryptresult, err := decrypt(restoreBase64Standard(unescapestr))
			if err != nil {
				return nil, err
			}
			newresult, _ := url.ParseQuery(decryptresult)
			l.roomID = newresult.Get("id")
		} else {
			roomid := result.Get("id")
			l.roomID = roomid
		}
	} else {
		//列表直播间地址
		paths := strings.Split(l.Url.Path, "/")
		if len(paths) < 2 {
			return nil, live.ErrRoomUrlIncorrect
		}
		urlparam := paths[2]
		//直接对roomid进行aes解密，解密成功则得到roomid，失败表示无需解密
		result, err := decrypt(restoreBase64Standard(urlparam))
		if err == nil {
			//需要截取 2081356094101258743?sign=64840db0d2bfc7f8d23c224a898cefa4
			l.roomID = strings.Split(result, "?")[0]
		} else {
			l.roomID = urlparam
		}
	}

	resp, err := requests.Get(roomInitUrl + l.roomID)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, live.ErrRoomNotExist
	}
	body, err := resp.Bytes()
	if err != nil || gjson.GetBytes(body, "h.code").Int() != 200 {
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
		HostName:     gjson.GetBytes(body, "b.userInfo.nickname").String(),
		RoomName:     gjson.GetBytes(body, "b.title").String(),
		Status:       gjson.GetBytes(body, "b.status").Int() == 4,
		CustomLiveId: "hongdoufm/" + l.roomID,
	}
	return info, nil
}

func (l *Live) GetStreamUrls() (us []*url.URL, err error) {
	body, err := l.getRoomInfo()
	if err != nil {
		return nil, live.ErrRoomNotExist
	}
	return utils.GenUrls(gjson.GetBytes(body, "b.flvPlayUrl").String())
}

func (l *Live) GetPlatformCNName() string {
	return cnName
}

// aes解密
//
//	@param ciphertext 待解密字符串
//	@return string 解密后字符串
//	@return error
func decrypt(ciphertext string) (string, error) {
	block, err := aes.NewCipher([]byte(secretkey))
	if err != nil {
		return "", err
	}
	decodedCiphertext, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		return "", err
	}
	decryptedData := make([]byte, len(decodedCiphertext))
	mode := cipher.NewCBCDecrypter(block, []byte(iv))
	mode.CryptBlocks(decryptedData, decodedCiphertext)
	return string(pkcs7Unpadding(decryptedData)), nil
}

func restoreBase64Standard(encryptstr string) string {
	tmp := strings.ReplaceAll(encryptstr, "-", "+")
	tmp = strings.ReplaceAll(tmp, "_", "/")
	return tmp
}

// 对使用PKCS7填充方式的数据进行去填充
func pkcs7Unpadding(data []byte) []byte {
	length := len(data)
	unpadding := int(data[length-1])
	return data[:(length - unpadding)]
}
