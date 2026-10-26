package main

import (
	"fmt"
	"io"
	"strings"
	"unicode"

	"github.com/mattlock83/wiretap/internal/assumptions"
)

// Human-readable reports sample details; their verdicts and totals always use
// the complete result. Check JSON retain every source value.
const maxTerminalItems = 10
const maxTerminalDetails = 3

func terminalLabel(value string) string {
	value = strings.Join(strings.FieldsFunc(value, unicode.IsControl), " ")
	return checkText(value, 240)
}

func terminalOmitted(out io.Writer, count int, label string) {
	if count > 0 {
		fmt.Fprintf(out, "  ... %d more %s; use --format json for complete details.\n", count, label)
	}
}

// Count the assurance of each matched example, independently of presentation
// limits. An unfamiliar kind must not silently acquire contract assurance.
type checkAssurance struct {
	httpValidation, httpProvider, exampleOnly, unknown int
}

func (counts *checkAssurance) add(matches []assumptions.Match) {
	for _, match := range matches {
		switch match.ContractCheck {
		case "openapi_3_0_validation_rules":
			counts.httpValidation++
		case "openapi_3_0_provider_contract":
			counts.httpProvider++
		case "unavailable":
			counts.exampleOnly++
		default:
			counts.unknown++
		}
	}
}

func (counts checkAssurance) String() string {
	return fmt.Sprintf("Assurance: %d HTTP declaration and value checks; %d HTTP provider-spec checks; %d example-only matches (schemas unavailable); %d matches with unknown assurance.", counts.httpValidation, counts.httpProvider, counts.exampleOnly, counts.unknown)
}

func checkText(value string, limit int) string {
	count := 0
	for index := range value {
		if count == limit {
			value = value[:index] + "… [truncated]"
			break
		}
		count++
	}
	return strings.Map(func(r rune) rune {
		if r == '\t' || r == '\n' || r == '\r' || r >= 0x20 && r != 0xfffe && r != 0xffff {
			return r
		}
		return '�'
	}, value)
}
