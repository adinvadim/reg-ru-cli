package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

const schemaVersion = "regru.cli/v1"

type outputMode uint8

const (
	outputHuman outputMode = iota
	outputJSON
	outputPlain
)

type successEnvelope struct {
	SchemaVersion string    `json:"schemaVersion"`
	OK            bool      `json:"ok"`
	Command       string    `json:"command"`
	Data          any       `json:"data"`
	Warnings      []Warning `json:"warnings"`
}

type errorEnvelope struct {
	SchemaVersion string       `json:"schemaVersion"`
	OK            bool         `json:"ok"`
	Command       string       `json:"command"`
	Error         errorPayload `json:"error"`
}

type errorPayload struct {
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	ExitCode  int            `json:"exitCode"`
	Retryable bool           `json:"retryable"`
	Details   map[string]any `json:"details"`
}

func writeResult(writer io.Writer, mode outputMode, command string, result Result) error {
	switch mode {
	case outputJSON:
		data := result.Data
		if data == nil {
			data = map[string]any{}
		}
		warnings := result.Warnings
		if warnings == nil {
			warnings = []Warning{}
		}
		return writeJSON(writer, successEnvelope{
			SchemaVersion: schemaVersion,
			OK:            true,
			Command:       command,
			Data:          data,
			Warnings:      warnings,
		})
	case outputPlain:
		for _, line := range result.Plain {
			if _, err := fmt.Fprintln(writer, line); err != nil {
				return err
			}
		}
		return nil
	default:
		if result.Human == "" {
			return nil
		}
		_, err := fmt.Fprintln(writer, result.Human)
		return err
	}
}

func writeError(
	writer io.Writer,
	mode outputMode,
	command string,
	cliErr *CLIError,
	color bool,
) error {
	switch mode {
	case outputJSON:
		details := cliErr.Details
		if details == nil {
			details = map[string]any{}
		}
		return writeJSON(writer, errorEnvelope{
			SchemaVersion: schemaVersion,
			OK:            false,
			Command:       command,
			Error: errorPayload{
				Code:      cliErr.Code,
				Message:   cliErr.Message,
				ExitCode:  cliErr.ExitCode,
				Retryable: cliErr.Retryable,
				Details:   details,
			},
		})
	case outputPlain:
		_, err := fmt.Fprintf(writer, "%s\t%s\n", cliErr.Code, singleLine(cliErr.Message))
		return err
	default:
		prefix := "Error"
		if color {
			prefix = "\x1b[31mError\x1b[0m"
		}
		_, err := fmt.Fprintf(writer, "%s [%s]: %s\n", prefix, cliErr.Code, cliErr.Message)
		return err
	}
}

func writeJSON(writer io.Writer, value any) error {
	encoder := json.NewEncoder(writer)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(value)
}

func singleLine(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func escapePlainField(value string) string {
	replacer := strings.NewReplacer(
		`\`, `\\`,
		"\t", `\t`,
		"\n", `\n`,
		"\r", `\r`,
	)
	return replacer.Replace(value)
}
