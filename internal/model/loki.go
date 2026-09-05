package model

import (
	"encoding/json"

	"github.com/invopop/jsonschema"
)

// ---- Loki HTTP API (/loki/api/v1/*) ----

// LokiResultType is the resultType of a Loki query response.
type LokiResultType string

// JSONSchema lists the three result types.
func (LokiResultType) JSONSchema() *jsonschema.Schema {
	return stringEnum("streams", "matrix", "vector")
}

// LokiResult is the `result` of a query: streams (log queries), a matrix
// (metric range queries) or a vector (metric instant queries and the volume
// endpoint), discriminated by the sibling resultType.
type LokiResult json.RawMessage

// MarshalJSON writes the raw value ("[]" when unset).
func (r LokiResult) MarshalJSON() ([]byte, error) {
	if len(r) == 0 {
		return []byte("[]"), nil
	}
	return json.RawMessage(r).MarshalJSON()
}

// UnmarshalJSON keeps the raw value.
func (r *LokiResult) UnmarshalJSON(b []byte) error {
	*r = append((*r)[:0], b...)
	return nil
}

// lokiPairSchema is a two-element array: [string, string] for stream values
// (nanosecond timestamp, line) or [number, string] for metric samples.
func lokiPairSchema(first string) *jsonschema.Schema {
	two := uint64(2)
	items := &jsonschema.Schema{Type: "string"}
	if first != "string" {
		items = &jsonschema.Schema{Extras: map[string]any{"type": []string{first, "string"}}}
	}
	return &jsonschema.Schema{Type: "array", Items: items, MinItems: &two, MaxItems: &two}
}

// JSONSchema declares the oneOf of streams, matrix and vector (contract §K.2.2).
func (LokiResult) JSONSchema() *jsonschema.Schema {
	labels := &jsonschema.Schema{Type: "object", AdditionalProperties: &jsonschema.Schema{Type: "string"}}
	streams := &jsonschema.Schema{Type: "array", Items: &jsonschema.Schema{
		Type:       "object",
		Properties: orderedProps("stream", labels, "values", &jsonschema.Schema{Type: "array", Items: lokiPairSchema("string")}),
		Required:   []string{"stream", "values"},
	}}
	matrix := &jsonschema.Schema{Type: "array", Items: &jsonschema.Schema{
		Type:       "object",
		Properties: orderedProps("metric", labels, "values", &jsonschema.Schema{Type: "array", Items: lokiPairSchema("number")}),
		Required:   []string{"metric", "values"},
	}}
	vector := &jsonschema.Schema{Type: "array", Items: &jsonschema.Schema{
		Type:       "object",
		Properties: orderedProps("metric", labels, "value", lokiPairSchema("number")),
		Required:   []string{"metric", "value"},
	}}
	return &jsonschema.Schema{OneOf: []*jsonschema.Schema{streams, matrix, vector}}
}

// LokiIngesterStats is Loki's ingester section (always zero: nothing is ingested at query time).
type LokiIngesterStats struct {
	CompressedBytes    int64 `json:"compressedBytes"`
	DecompressedBytes  int64 `json:"decompressedBytes"`
	DecompressedLines  int64 `json:"decompressedLines"`
	HeadChunkBytes     int64 `json:"headChunkBytes"`
	HeadChunkLines     int64 `json:"headChunkLines"`
	TotalBatches       int64 `json:"totalBatches"`
	TotalChunksMatched int64 `json:"totalChunksMatched"`
	TotalDuplicates    int64 `json:"totalDuplicates"`
	TotalLinesSent     int64 `json:"totalLinesSent"`
	TotalReached       int64 `json:"totalReached"`
}

// LokiStoreStats is Loki's store section; totalChunksRef counts the streams selected.
type LokiStoreStats struct {
	CompressedBytes       int64   `json:"compressedBytes"`
	DecompressedBytes     int64   `json:"decompressedBytes"`
	DecompressedLines     int64   `json:"decompressedLines"`
	ChunksDownloadTime    float64 `json:"chunksDownloadTime"`
	TotalChunksRef        int64   `json:"totalChunksRef"`
	TotalChunksDownloaded int64   `json:"totalChunksDownloaded"`
	TotalDuplicates       int64   `json:"totalDuplicates"`
}

// LokiSummaryStats is Loki's summary section.
type LokiSummaryStats struct {
	BytesProcessedPerSecond int64   `json:"bytesProcessedPerSecond"`
	ExecTime                float64 `json:"execTime"`
	LinesProcessedPerSecond int64   `json:"linesProcessedPerSecond"`
	QueueTime               float64 `json:"queueTime"`
	TotalBytesProcessed     int64   `json:"totalBytesProcessed"`
	TotalLinesProcessed     int64   `json:"totalLinesProcessed"`
	TotalEntriesReturned    int64   `json:"totalEntriesReturned"`
}

// LokiStats is the stats block of a query response.
type LokiStats struct {
	Ingester LokiIngesterStats `json:"ingester"`
	Store    LokiStoreStats    `json:"store"`
	Summary  LokiSummaryStats  `json:"summary"`
}

// LokiQueryData is the data of /loki/api/v1/query_range and /loki/api/v1/query.
type LokiQueryData struct {
	ResultType LokiResultType `json:"resultType"`
	Result     LokiResult     `json:"result"`
	Stats      LokiStats      `json:"stats"`
}

// LokiQueryResult is the success envelope of /loki/api/v1/query_range
// (streams or matrix) and /loki/api/v1/query (streams or vector).
type LokiQueryResult struct {
	Status string        `json:"status"`
	Data   LokiQueryData `json:"data"`
}

// LokiLabelsResult is the body of /loki/api/v1/labels and /loki/api/v1/label/{name}/values.
type LokiLabelsResult struct {
	Status string   `json:"status"`
	Data   []string `json:"data"`
}

// LokiSeriesResult is the body of /loki/api/v1/series: one label set per stream.
type LokiSeriesResult struct {
	Status string              `json:"status"`
	Data   []map[string]string `json:"data"`
}

// LokiIndexStats is the body of /loki/api/v1/index/stats (no envelope, Loki's shape).
type LokiIndexStats struct {
	Streams uint64 `json:"streams"`
	Chunks  uint64 `json:"chunks"`
	Entries uint64 `json:"entries"`
	Bytes   uint64 `json:"bytes"`
}

// LokiVolumeData is the data of /loki/api/v1/index/volume: a vector of bytes per series or label.
type LokiVolumeData struct {
	ResultType LokiResultType `json:"resultType"`
	Result     LokiResult     `json:"result"`
}

// LokiVolumeResult is the body of /loki/api/v1/index/volume.
type LokiVolumeResult struct {
	Status string         `json:"status"`
	Data   LokiVolumeData `json:"data"`
}

// LokiBuildInfo is the body of /loki/api/v1/status/buildinfo (no envelope).
type LokiBuildInfo struct {
	Version   string `json:"version"`
	Revision  string `json:"revision"`
	Branch    string `json:"branch"`
	BuildUser string `json:"buildUser"`
	BuildDate string `json:"buildDate"`
	GoVersion string `json:"goVersion"`
}
