package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/phrond/wiretap/internal/knowledge"
)

func knowledgeCommand(command string, args []string) error {
	f := flag.NewFlagSet("wiretap "+command, flag.ContinueOnError)
	input := f.String("bundle", "", "source run archive (publish/check) or knowledge bundle (validate)")
	config := f.String("config", "", "publication identity and selection configuration")
	output := f.String("output", "docs/knowledge", "maintained repository bundle directory")
	if err := f.Parse(args); err != nil {
		return err
	}
	if len(f.Args()) != 0 || *input == "" {
		return fmt.Errorf("%w: --bundle is required and positional arguments are unsupported", errUsage)
	}
	if command == "validate" {
		v := knowledge.Validate(*input)
		b, err := knowledge.Canonical(v)
		if err != nil {
			return err
		}
		if _, err := os.Stdout.Write(b); err != nil {
			return err
		}
		if !v.Valid() {
			return fmt.Errorf("bundle validation failed")
		}
		return nil
	}
	if *config == "" {
		return fmt.Errorf("%w: --config is required for stable publication identities and selection", errUsage)
	}
	c, err := knowledge.ReadConfig(*config)
	if err != nil {
		return err
	}
	result, err := knowledge.Publish(*input, *output, c, command == "check")
	if err != nil {
		return err
	}
	b, err := knowledge.Canonical(result)
	if err != nil {
		return err
	}
	if _, err := os.Stdout.Write(b); err != nil {
		return err
	}
	if command == "check" && len(result.Changes) > 0 {
		return fmt.Errorf("publication differs: %d paths; run wiretap publish with these inputs", len(result.Changes))
	}
	return nil
}
