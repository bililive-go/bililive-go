package acfun

import (
	"container/heap"
	"encoding/json"
	"net/url"

	"github.com/bililive-go/bililive-go/src/pkg/utils"
)

type representation struct {
	Url   string `json:"url"`
	Level int    `json:"level"`
}

type representations []representation

func (r representations) Len() int { return len(r) }

func (r representations) Less(i, j int) bool {
	return r[i].Level > r[j].Level
}

func (r representations) Swap(i, j int) {
	r[i], r[j] = r[j], r[i]
}

func (r *representations) Push(x interface{}) {
	*r = append(*r, x.(representation))
}

func (r *representations) Pop() interface{} {
	old := *r
	n := len(old)
	item := old[n-1]
	*r = old[0 : n-1]
	return item
}

func (r representations) GenUrls() ([]*url.URL, error) {
	urls := make([]string, r.Len())
	for idx, item := range r {
		urls[idx] = item.Url
	}
	return utils.GenUrls(urls...)
}

func newRepresentationsFromJSON(s string) (representations, error) {
	rs := make(representations, 0)
	if err := json.Unmarshal([]byte(s), &rs); err != nil {
		return nil, err
	}
	heap.Fix(&rs, rs.Len()-1)
	return rs, nil
}
