package bilibili

// DanmakuMsg 弹幕消息
type DanmakuMsg struct {
	Content     string
	UID         int64
	Uname       string
	GuardLevel  int // 0=无, 1=总督, 2=提督, 3=舰长
	Color       int
	Timestamp   int64
	MedalLevel  int
	MedalName   string
	MedalUpName string
}

// GiftMsg 礼物消息
type GiftMsg struct {
	UID       int64
	Uname     string
	GiftName  string
	Num       int
	GiftID    int
	CoinType  string // "gold" 或 "silver"
	Price     int
	Timestamp int64
}

// SuperChatMsg 醒目留言（SC）
type SuperChatMsg struct {
	UID     int64
	Uname   string
	Message string
	Price   int
}

// GuardBuyMsg 舰长购买
type GuardBuyMsg struct {
	UID        int64
	Username   string
	GiftName   string
	GuardLevel int // 1=总督, 2=提督, 3=舰长
	Num        int
	Price      int
}

// HostInfo 弹幕服务器信息
type HostInfo struct {
	Host    string `json:"host"`
	Port    int    `json:"port"`
	WssPort int    `json:"wss_port"`
	WsPort  int    `json:"ws_port"`
}

// DanmuInfoResponse getDanmuInfo API 响应
type DanmuInfoResponse struct {
	Code int `json:"code"`
	Data struct {
		Token    string     `json:"token"`
		HostList []HostInfo `json:"host_list"`
	} `json:"data"`
}

// RoomInitResponse room_init API 响应
type RoomInitResponse struct {
	Code int `json:"code"`
	Data struct {
		RoomID int `json:"room_id"`
	} `json:"data"`
}

// NavResponse nav API 响应（获取 UID 和 WbiKeys）
type NavResponse struct {
	Code int `json:"code"`
	Data struct {
		Mid    int `json:"mid"`
		WbiImg struct {
			ImgURL string `json:"img_url"`
			SubURL string `json:"sub_url"`
		} `json:"wbi_img"`
	} `json:"data"`
}
