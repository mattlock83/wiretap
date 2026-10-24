package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"

	"github.com/mattlock83/wiretap/internal/version"
)

func versionCommand(args []string, out io.Writer) error {
	flags := flag.NewFlagSet("wiretap version", flag.ContinueOnError)
	flags.SetOutput(out)
	jsonOutput := flags.Bool("json", false, "write producer identity as JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 0 {
		return fmt.Errorf("wiretap version accepts no positional arguments")
	}
	producer := version.Current()
	if *jsonOutput {
		return json.NewEncoder(out).Encode(producer)
	}
	label := producer.Name + " " + producer.Version
	if producer.Commit != "" {
		label += " (" + producer.Commit + ")"
	}
	if producer.Dirty {
		label += " dirty"
	}
	_, err := fmt.Fprintln(out, label)
	return err
}
