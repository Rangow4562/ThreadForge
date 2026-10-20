package service

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"

	otlplogs "go.opentelemetry.io/proto/otlp/logs/v1"
	"google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	beacon "github.com/asymptote-labs/agent-beacon/pkg/asymptoteobserve"
)

type otlpEncoding uint8

const (
	otlpJSON otlpEncoding = iota
	otlpProtobuf
)

func parseOTLPEncoding(header string) (otlpEncoding, error) {
	if strings.TrimSpace(header) == "" {
		return otlpJSON, nil
	}
	mediaType, _, err := mime.ParseMediaType(header)
	if err != nil {
		return otlpJSON, err
	}
	switch strings.ToLower(mediaType) {
	case "application/json":
		return otlpJSON, nil
	case "application/x-protobuf":
		return otlpProtobuf, nil
	default:
		return otlpJSON, errors.New("unsupported OTLP media type")
	}
}

func readBoundedOTLPBody(request *http.Request) ([]byte, error) {
	defer request.Body.Close()
	compressed, err := io.ReadAll(io.LimitReader(request.Body, maxRequestBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(compressed)) > maxRequestBytes {
		return nil, errors.New("compressed OTLP body exceeds the request limit")
	}
	encoding := strings.ToLower(strings.TrimSpace(request.Header.Get("Content-Encoding")))
	if encoding == "" || encoding == "identity" {
		return compressed, nil
	}
	if encoding != "gzip" {
		return nil, errors.New("unsupported OTLP content encoding")
	}
	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	decoded, err := io.ReadAll(io.LimitReader(reader, maxRequestBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(decoded)) > maxRequestBytes {
		return nil, errors.New("decompressed OTLP body exceeds the request limit")
	}
	return decoded, nil
}

func decodeOTLPProtobufLogs(data []byte) ([]beacon.Event, error) {
	request := &otlplogs.LogsData{}
	if err := proto.Unmarshal(data, request); err != nil {
		return nil, err
	}
	jsonBody, err := (protojson.MarshalOptions{UseEnumNumbers: true}).Marshal(request)
	if err != nil {
		return nil, err
	}
	return decodeOTLPLogs(jsonBody)
}

func writeOTLPResponse(w http.ResponseWriter, encoding otlpEncoding, statusCode int, message string) {
	var body []byte
	var err error
	if message == "" {
		if encoding == otlpProtobuf {
			// ExportLogsServiceResponse is an empty message on full success.
			body = []byte{}
		} else {
			body = []byte("{}")
		}
	} else {
		code := int32(13) // google.rpc.Code.INTERNAL
		if statusCode == http.StatusBadRequest || statusCode == http.StatusUnsupportedMediaType {
			code = 3 // google.rpc.Code.INVALID_ARGUMENT
		}
		response := &status.Status{Code: code, Message: message}
		if encoding == otlpProtobuf {
			body, err = proto.Marshal(response)
		} else {
			body, err = (protojson.MarshalOptions{}).Marshal(response)
		}
	}
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	if encoding == otlpProtobuf {
		w.Header().Set("Content-Type", "application/x-protobuf")
	} else {
		w.Header().Set("Content-Type", "application/json")
	}
	w.WriteHeader(statusCode)
	_, _ = w.Write(body)
}
