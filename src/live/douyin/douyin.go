package douyin

import (
<<<<<<< HEAD
	"fmt"
	"net/http"
	"net/url"
	"sort"
=======
	"net/url"
>>>>>>> origin/master

	"github.com/bililive-go/bililive-go/src/live"
	"github.com/bililive-go/bililive-go/src/live/internal"
)

const (
	domain       = "live.douyin.com"
	domainForApp = "v.douyin.com"
	cnName       = "抖音"
)

func init() {
	live.Register(domain, new(builder))
	live.Register(domainForApp, new(builder))
}

type builder struct{}

func (b *builder) Build(url *url.URL) (live.Live, error) {
	ret := &Live{
		BaseLive: internal.NewBaseLive(url),
	}
	ret.btoolsLive = NewBtoolsLive(ret)
	return ret, nil
}

type Live struct {
	internal.BaseLive
<<<<<<< HEAD
	LastAvailableStreamData streamData
	bgoLive                 bgoLive
	btoolsLive              btoolsLive
}


// 检查URL可用性的函数
func (l *Live) checkUrlAvailability(urlStr string) bool {
	// 简单的HEAD请求检查
	client := &http.Client{Timeout: 5 * 1000000000} // 5秒超时
	resp, err := client.Head(urlStr)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// 获取质量索引
func getQualityIndex(quality string) (string, int) {
	qualityMap := map[string]int{
		"origin": 0,
		"uhd":    1,
		"hd":     2,
		"sd":     3,
		"ld":     4,
	}
	if index, exists := qualityMap[quality]; exists {
		return quality, index
	}
	return "hd", 2 // 默认返回hd质量
}

func (l *Live) createStreamUrlInfos(streamUrlInfo, originUrlList map[string]interface{}) ([]live.StreamUrlInfo, error) {
	// 构建流URL信息
	streamUrlInfos := make([]live.StreamUrlInfo, 0, 10)

	// 处理FLV URL
	if flvPullUrl, ok := streamUrlInfo["flv_pull_url"].(map[string]interface{}); ok {
		var flvUrls []string
		var flvQualities []string

		// 如果有origin URL，添加到开头
		if originUrlList != nil {
			if originFlv, ok := originUrlList["flv"].(string); ok {
				// 添加codec参数
				originFlvWithCodec := originFlv
				if sdkParams, ok := originUrlList["sdk_params"].(map[string]interface{}); ok {
					if vCodec, ok := sdkParams["VCodec"].(string); ok {
						originFlvWithCodec += "&codec=" + vCodec
					}
				}
				flvUrls = append(flvUrls, originFlvWithCodec)
				flvQualities = append(flvQualities, "ORIGIN")
			}
		}

		// 添加其他FLV流
		for quality, urlStr := range flvPullUrl {
			if urlStrStr, ok := urlStr.(string); ok {
				flvUrls = append(flvUrls, urlStrStr)
				flvQualities = append(flvQualities, quality)
			}
		}

		// 补齐逻辑：如果FLV URL数量少于5个，用最后一个补齐
		for len(flvUrls) < 5 {
			if len(flvUrls) > 0 {
				flvUrls = append(flvUrls, flvUrls[len(flvUrls)-1])
				flvQualities = append(flvQualities, flvQualities[len(flvQualities)-1])
			}
		}

		// 将补齐后的URL添加到streamUrlInfos
		for i, urlStr := range flvUrls {
			url, err := url.Parse(urlStr)
			if err != nil {
				continue
			}
			quality := flvQualities[i]
			streamUrlInfos = append(streamUrlInfos, live.StreamUrlInfo{
				Name:        quality,
				Description: fmt.Sprintf("FLV Stream - %s", quality),
				Url:         url,
				Resolution:  0,
				Vbitrate:    0,
			})
		}
	}

	// 处理HLS URL
	if hlsPullUrlMap, ok := streamUrlInfo["hls_pull_url_map"].(map[string]interface{}); ok {
		var hlsUrls []string
		var hlsQualities []string

		// 如果有origin URL，添加到开头
		if originUrlList != nil {
			if originHls, ok := originUrlList["hls"].(string); ok {
				// 添加codec参数
				originHlsWithCodec := originHls
				if sdkParams, ok := originUrlList["sdk_params"].(map[string]interface{}); ok {
					if vCodec, ok := sdkParams["VCodec"].(string); ok {
						originHlsWithCodec += "&codec=" + vCodec
					}
				}
				hlsUrls = append(hlsUrls, originHlsWithCodec)
				hlsQualities = append(hlsQualities, "ORIGIN")
			}
		}

		// 添加其他HLS流
		for quality, urlStr := range hlsPullUrlMap {
			if urlStrStr, ok := urlStr.(string); ok {
				hlsUrls = append(hlsUrls, urlStrStr)
				hlsQualities = append(hlsQualities, quality)
			}
		}

		// 补齐逻辑：如果HLS URL数量少于5个，用最后一个补齐
		for len(hlsUrls) < 5 {
			if len(hlsUrls) > 0 {
				hlsUrls = append(hlsUrls, hlsUrls[len(hlsUrls)-1])
				hlsQualities = append(hlsQualities, hlsQualities[len(hlsQualities)-1])
			}
		}

		// 将补齐后的URL添加到streamUrlInfos
		for i, urlStr := range hlsUrls {
			url, err := url.Parse(urlStr)
			if err != nil {
				continue
			}
			quality := hlsQualities[i]
			streamUrlInfos = append(streamUrlInfos, live.StreamUrlInfo{
				Name:        quality + "_HLS",
				Description: fmt.Sprintf("HLS Stream - %s", quality),
				Url:         url,
				Resolution:  0,
				Vbitrate:    0,
			})
		}
	}

	// 按分辨率排序（如果有的话）
	sort.Slice(streamUrlInfos, func(i, j int) bool {
		if streamUrlInfos[i].Resolution != streamUrlInfos[j].Resolution {
			return streamUrlInfos[i].Resolution > streamUrlInfos[j].Resolution
		} else {
			return streamUrlInfos[i].Vbitrate > streamUrlInfos[j].Vbitrate
		}
	})
	// TODO: fix inefficient code
	//nolint:ineffassign

	return streamUrlInfos, nil
=======
	btoolsLive btoolsLive
>>>>>>> origin/master
}

func (l *Live) GetInfo() (info *live.Info, err error) {
	return l.btoolsLive.GetInfo()
}

func (l *Live) GetStreamInfos() (us []*live.StreamUrlInfo, err error) {
	return l.btoolsLive.GetStreamInfos()
}

func (l *Live) GetPlatformCNName() string {
	return cnName
}
