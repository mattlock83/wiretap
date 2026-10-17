package postman

// Collection and its contents use the Postman Collection v2.1 JSON format.
type Collection struct {
	Info                    Info             `json:"info"`
	Items                   []Folder         `json:"item"`
	ProtocolProfileBehavior ProtocolBehavior `json:"protocolProfileBehavior"`
}

// Export is one independently importable collection and its suggested filename.
type Export struct {
	Filename   string
	Collection *Collection
	GRPC       *GRPCCollection
}

type ProtocolBehavior struct {
	FollowRedirects       bool            `json:"followRedirects"`
	DisableBodyPruning    bool            `json:"disableBodyPruning"`
	DisableCookies        bool            `json:"disableCookies"`
	DisableURLEncoding    bool            `json:"disableUrlEncoding"`
	DisabledSystemHeaders map[string]bool `json:"disabledSystemHeaders"`
}

type Info struct {
	Name        string `json:"name"`
	Schema      string `json:"schema"`
	Description string `json:"description"`
}

type Folder struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Items       []Item `json:"item"`
}

type Item struct {
	Name      string     `json:"name"`
	Request   Request    `json:"request"`
	Responses []Response `json:"response"`
	Events    []Event    `json:"event"`
}

type Request struct {
	Method      string       `json:"method"`
	URL         string       `json:"url"`
	Headers     []Header     `json:"header"`
	Body        *RequestBody `json:"body,omitempty"`
	Description string       `json:"description,omitempty"`
}

type Header struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type RequestBody struct {
	Mode    string       `json:"mode"`
	Raw     *string      `json:"raw,omitempty"`
	File    *File        `json:"file,omitempty"`
	Options *BodyOptions `json:"options,omitempty"`
}

type BodyOptions struct {
	Raw RawOptions `json:"raw"`
}

type RawOptions struct {
	Language string `json:"language"`
}

type File struct {
	Src     *string `json:"src"`
	Content string  `json:"content"`
}

type Response struct {
	Name            string   `json:"name"`
	OriginalRequest Request  `json:"originalRequest"`
	Status          string   `json:"status,omitempty"`
	Code            int      `json:"code,omitempty"`
	Headers         []Header `json:"header"`
	Body            string   `json:"body"`
	ResponseTime    int64    `json:"responseTime"`
}

type Event struct {
	Listen string `json:"listen"`
	Script Script `json:"script"`
}

type Script struct {
	Type string   `json:"type"`
	Exec []string `json:"exec"`
}
