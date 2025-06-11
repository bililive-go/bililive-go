package servers

import (
	"github.com/bililive-go/bililive-go/src/live"
)

type commonResp struct {
	ErrNo  int         `json:"err_no"`
	ErrMsg string      `json:"err_msg"`
	Data   interface{} `json:"data"`
}

type liveSlice []*live.Info

func (c liveSlice) Len() int {
	return len(c)
}
func (c liveSlice) Swap(i, j int) {
	c[i], c[j] = c[j], c[i]
}
func (c liveSlice) Less(i, j int) bool {
	return c[i].Live.GetLiveId() < c[j].Live.GetLiveId()
}
