package aliyun

// 以下是 PushV2 的 PushTask 中本包用到的字段，名称与接口文档一致：
// https://help.aliyun.com/zh/document_detail/2979224.html

// pushTask 是阿里云推送接口的一个推送任务。
type pushTask struct {
	Target       target       `json:"Target"`
	Notification notification `json:"Notification"`
	Options      options      `json:"Options"`
}

// target 是推送的目标。
type target struct {
	Type  string `json:"Type"`
	Value string `json:"Value"`
}

// notification 是通知的内容和各平台的设置。
type notification struct {
	Title     string               `json:"Title"`
	Body      string               `json:"Body"`
	Android   *androidNotification `json:"Android,omitempty"`
	IOS       *iosNotification     `json:"Ios,omitempty"`
	HarmonyOS *harmonyNotification `json:"Hmos,omitempty"`
}

// androidNotification 是 Android 通知的设置。
type androidNotification struct {
	VendorChannelActivity string          `json:"VendorChannelActivity,omitempty"`
	ChannelID             string          `json:"ChannelId,omitempty"`
	ExtParameters         string          `json:"ExtParameters,omitempty"`
	BadgeSetNum           int             `json:"BadgeSetNum,omitempty"`
	Options               *androidOptions `json:"Options,omitempty"`
}

// androidOptions 是 Android 各厂商通道的设置。
type androidOptions struct {
	Huawei *vendorCategory `json:"Huawei,omitempty"`
	Honor  *honorOptions   `json:"Honor,omitempty"`
	Vivo   *vendorCategory `json:"Vivo,omitempty"`
	Oppo   *vendorCategory `json:"Oppo,omitempty"`
	Xiaomi *xiaomiOptions  `json:"Xiaomi,omitempty"`
}

// vendorCategory 是厂商的消息分类。
type vendorCategory struct {
	Category string `json:"Category"`
}

// honorOptions 是荣耀通道的设置。
type honorOptions struct {
	Importance int `json:"Importance"`
}

// xiaomiOptions 是小米通道的设置。
type xiaomiOptions struct {
	Channel string `json:"Channel"`
}

// iosNotification 是 iOS 通知的设置。
type iosNotification struct {
	ApnsEnv       string `json:"ApnsEnv"`
	Music         string `json:"Music"`
	Badge         *int   `json:"Badge,omitempty"`
	ExtParameters string `json:"ExtParameters,omitempty"`
}

// harmonyNotification 是鸿蒙通知的设置。
type harmonyNotification struct {
	Category      string `json:"Category,omitempty"`
	BadgeSetNum   *int   `json:"BadgeSetNum,omitempty"`
	ExtParameters string `json:"ExtParameters,omitempty"`
}

// options 是推送任务的选项：过期时间、任务标识等。
type options struct {
	ExpireTime string `json:"ExpireTime,omitempty"`
	JobKey     string `json:"JobKey,omitempty"`
	Trim       bool   `json:"Trim,omitempty"`
}
