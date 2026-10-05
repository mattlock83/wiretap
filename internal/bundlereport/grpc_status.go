package bundlereport

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/phrond/wiretap/internal/artifact"
)

type viewerStatusDetails struct {
	State, Source, Code, Message, Diagnostic string
	Attachments                              []viewerAttachment
	Details                                  []viewerStatusDetail
}
type viewerStatusDetail struct {
	Index                         int
	Type, State, Diagnostic, Body string
	Attachments                   []viewerAttachment
}

func statusAttachment(body *artifact.Body, prefix, label string) *viewerAttachment {
	if body == nil {
		return nil
	}
	raw := []byte(body.Text)
	if body.Encoding == "base64" {
		var err error
		raw, err = base64.StdEncoding.DecodeString(body.Bytes)
		if err != nil {
			return nil
		}
	}
	hash := sha256.Sum256(raw)
	return &viewerAttachment{Label: label, URL: prefix + "payloads/" + hex.EncodeToString(hash[:]) + ".bin"}
}
func viewerRichStatus(status *artifact.GrpcStatusDetails) *viewerStatusDetails {
	if status == nil {
		return nil
	}
	view := &viewerStatusDetails{State: status.State, Source: status.Source, Message: status.Message, Diagnostic: status.Diagnostic}
	if status.Code != nil {
		view.Code = grpcStatusLabel(*status.Code)
	}
	for _, attachment := range []*viewerAttachment{statusAttachment(status.Wire, "../", "Status envelope"), statusAttachment(status.AuxiliaryDescriptor, "../", "Auxiliary error descriptors")} {
		if attachment != nil {
			view.Attachments = append(view.Attachments, *attachment)
		}
	}
	for _, detail := range status.Details {
		d := viewerStatusDetail{Index: detail.Index, Type: detail.Type, State: detail.State, Diagnostic: detail.Diagnostic}
		if d.Type == "" {
			d.Type = detail.TypeURL
		}
		if detail.Body != nil {
			d.Body = viewerPreview(detail.Body.Text)
		}
		for _, attachment := range []*viewerAttachment{statusAttachment(detail.Wire, "../", "Detail wire bytes"), statusAttachment(detail.Body, "../", "Detail JSON")} {
			if attachment != nil {
				d.Attachments = append(d.Attachments, *attachment)
			}
		}
		view.Details = append(view.Details, d)
	}
	return view
}

func writeRichStatus(page *strings.Builder, status *artifact.GrpcStatusDetails) {
	if status == nil {
		return
	}
	fmt.Fprintf(page, "#### RPC error details\n\nObserved in %s; retention/decoding: **%s**. These are outcome details, not application response messages or declared coverage targets.\n\n", escape(status.Source), escape(status.State))
	if status.Code != nil {
		fmt.Fprintf(page, "Envelope status: **%s**. The terminal RPC status remains authoritative.\n\n", grpcStatusLabel(*status.Code))
	}
	if status.Message != "" {
		fmt.Fprintf(page, "Status message: %s\n\n", escape(status.Message))
	}
	if status.Diagnostic != "" {
		fmt.Fprintf(page, "%s\n\n", escape(status.Diagnostic))
	}
	for _, attachment := range []*viewerAttachment{statusAttachment(status.Wire, "../../", "Retained status envelope"), statusAttachment(status.AuxiliaryDescriptor, "../../", "Frozen auxiliary error descriptors")} {
		if attachment != nil {
			fmt.Fprintf(page, "[%s](%s).\n\n", attachment.Label, attachment.URL)
		}
	}
	for _, detail := range status.Details {
		name := detail.Type
		if name == "" {
			name = detail.TypeURL
		}
		fmt.Fprintf(page, "##### Detail %d: %s\n\nState: **%s**.\n\n", detail.Index, escape(name), escape(detail.State))
		if detail.Diagnostic != "" {
			fmt.Fprintf(page, "%s\n\n", escape(detail.Diagnostic))
		}
		if detail.Body != nil {
			page.WriteString(preview(detail.Body.Text))
		}
		for _, attachment := range []*viewerAttachment{statusAttachment(detail.Wire, "../../", "Detail wire bytes"), statusAttachment(detail.Body, "../../", "Detail JSON")} {
			if attachment != nil {
				fmt.Fprintf(page, "[%s](%s).\n\n", attachment.Label, attachment.URL)
			}
		}
	}
}

type knowledgeStatusDetails struct {
	State      string                  `json:"state"`
	Code       *int                    `json:"code,omitempty"`
	Message    string                  `json:"message,omitempty"`
	Diagnostic string                  `json:"diagnostic,omitempty"`
	Details    []knowledgeStatusDetail `json:"details"`
}
type knowledgeStatusDetail struct {
	Index      int    `json:"index"`
	TypeURL    string `json:"type_url"`
	Type       string `json:"type,omitempty"`
	State      string `json:"state"`
	Diagnostic string `json:"diagnostic,omitempty"`
	Projection string `json:"projection,omitempty"`
	Wire       string `json:"wire,omitempty"`
}

func knowledgeRichStatus(status *artifact.GrpcStatusDetails) *knowledgeStatusDetails {
	if status == nil {
		return nil
	}
	result := &knowledgeStatusDetails{State: status.State, Code: status.Code, Message: status.Message, Diagnostic: status.Diagnostic, Details: []knowledgeStatusDetail{}}
	for _, detail := range status.Details {
		d := knowledgeStatusDetail{Index: detail.Index, TypeURL: detail.TypeURL, Type: detail.Type, State: detail.State, Diagnostic: detail.Diagnostic}
		if attachment := statusAttachment(detail.Body, "", ""); attachment != nil {
			d.Projection = attachment.URL
		}
		if attachment := statusAttachment(detail.Wire, "", ""); attachment != nil {
			d.Wire = attachment.URL
		}
		result.Details = append(result.Details, d)
	}
	return result
}
