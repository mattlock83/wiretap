package postman

// These types follow Postman's official v3.0.0 JSON schema. A v3 folder has
// $kind "collection", just like its root. Contracts are supplied separately.
type GRPCCollection struct {
	Kind        string       `json:"$kind"`
	Name        string       `json:"name"`
	Description string       `json:"description,omitempty"`
	Items       []GRPCFolder `json:"items"`
}

type GRPCFolder struct {
	Kind  string        `json:"$kind"`
	Name  string        `json:"name"`
	Items []GRPCRequest `json:"items"`
}

type GRPCCall struct {
	MethodPath string       `json:"methodPath"`
	Metadata   []Header     `json:"metadata"`
	Message    *GRPCContent `json:"message,omitempty"`
}

type GRPCRequest struct {
	Kind string `json:"$kind"`
	ID   string `json:"id"`
	Name string `json:"name"`
	GRPCCall
	Examples []GRPCExample `json:"examples"`
}

type GRPCContent struct {
	Content string `json:"content"`
}

type GRPCExample struct {
	Kind     string       `json:"$kind"`
	Name     string       `json:"name"`
	Request  GRPCCall     `json:"request"`
	Response GRPCResponse `json:"response"`
}

type GRPCResponse struct {
	Code     int           `json:"code"`
	Metadata []Header      `json:"metadata"`
	Trailers []Header      `json:"trailers"`
	Messages []GRPCMessage `json:"messages"`
}

type GRPCMessage struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Content string `json:"content"`
}
