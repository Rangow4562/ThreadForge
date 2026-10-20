package migrate

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"math"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/threadforge-ai/threadforge/pkg/privacy"
)

const (
	anyJevDistillationCacheSchema = "threadforge.anyjev-distillation-cache.v1"
	anyJevNPZMaxEntries           = 4
	anyJevNPZMaxExpandedBytes     = 32 << 20
	anyJevNPYMaxHeaderBytes       = 64 << 10
	anyJevNPYMaxElements          = 5_000_000
)

var (
	anyJevNPYHeaderPattern = regexp.MustCompile(`^\s*\{\s*['"]descr['"]\s*:\s*['"]([<>=|])?([A-Za-z]+[0-9]+)['"]\s*,\s*['"]fortran_order['"]\s*:\s*(True|False)\s*,\s*['"]shape['"]\s*:\s*\(([^)]*)\)\s*,?\s*\}\s*$`)
)

type anyJevDistillationCacheImport struct {
	SchemaVersion       string   `json:"schema_version"`
	CacheKind           string   `json:"cache_kind"`
	ModelSHA256         string   `json:"model_sha256"`
	QuestionSHA256      string   `json:"question_sha256"`
	Split               string   `json:"split"`
	Examples            int      `json:"examples"`
	ClassCount          int      `json:"class_count"`
	Block               int      `json:"block"`
	FeatureWidth        int      `json:"feature_width,omitempty"`
	MeanTopConfidence   *float64 `json:"mean_top_confidence,omitempty"`
	MeanEntropy         *float64 `json:"mean_entropy,omitempty"`
	ArgmaxCounts        []int    `json:"argmax_counts,omitempty"`
	Top1Accuracy        *float64 `json:"top1_accuracy,omitempty"`
	ExpectedCalibration *float64 `json:"expected_calibration_error,omitempty"`
	NegativeLogLoss     *float64 `json:"negative_log_loss,omitempty"`
	OmittedFields       []string `json:"omitted_fields"`
	HumanReviewRequired bool     `json:"human_review_required"`
	ExecutionVerified   bool     `json:"execution_verified"`
	CutoverEligible     bool     `json:"cutover_evidence_eligible"`
}

type anyJevNPYArray struct {
	kind      byte
	itemSize  int
	byteOrder byte
	shape     []int
	data      []byte
}

func isAnyJevDistillationNPZPath(relative string) bool {
	clean := strings.Trim(filepathSlash(relative), "/")
	parts := strings.Split(clean, "/")
	if len(parts) != 5 || !strings.EqualFold(parts[0], "bench") ||
		!strings.EqualFold(parts[1], "results_distill") ||
		!(strings.EqualFold(parts[2], "synth_labels") || strings.EqualFold(parts[2], "synth_features")) ||
		strings.TrimSpace(parts[3]) == "" || !strings.EqualFold(npzPathExt(parts[4]), ".npz") ||
		len(parts[4]) <= len(".npz") {
		return false
	}
	return true
}

func npzPathExt(value string) string {
	index := strings.LastIndexByte(value, '.')
	if index < 0 {
		return ""
	}
	return value[index:]
}

func normalizeAnyJevDistillationNPZ(relative string, data []byte) (normalizedItem, error) {
	if !isAnyJevDistillationNPZPath(relative) || len(data) == 0 || len(data) > maxFileBytes {
		return normalizedItem{}, errors.New("unsupported AnyJev distillation cache path or size")
	}
	parts := strings.Split(strings.Trim(filepathSlash(relative), "/"), "/")
	model := parts[3]
	base := strings.TrimSuffix(parts[4], npzPathExt(parts[4]))
	separator := strings.LastIndexByte(base, '.')
	if separator <= 0 {
		return normalizedItem{}, errors.New("AnyJev distillation cache filename is invalid")
	}
	question, split := base[:separator], strings.ToLower(base[separator+1:])
	if _, ok := boundedAnyJevBenchmarkString(model, 2048); !ok || privacy.ContainsCredentialText(model) ||
		strings.Contains(model, "..") || strings.ContainsAny(model, `/\`) {
		return normalizedItem{}, errors.New("AnyJev distillation model identifier is invalid")
	}
	if _, ok := boundedAnyJevBenchmarkString(question, 512); !ok || privacy.ContainsCredentialText(question) ||
		strings.Contains(question, "..") || strings.ContainsAny(question, `/\`) {
		return normalizedItem{}, errors.New("AnyJev distillation question identifier is invalid")
	}
	if split != "synth" && split != "test" {
		return normalizedItem{}, errors.New("AnyJev distillation split is unsupported")
	}

	arrays, err := readAnyJevNPZ(data)
	if err != nil {
		return normalizedItem{}, err
	}
	kArray, blockArray := arrays["K"], arrays["block"]
	classCount, classOK := anyJevNPYIntegerScalar(kArray)
	block, blockOK := anyJevNPYIntegerScalar(blockArray)
	if !classOK || classCount < 2 || classCount > 512 || !blockOK || block < 1 || block > 100000 {
		return normalizedItem{}, errors.New("AnyJev distillation cache K or block is invalid")
	}

	projected := anyJevDistillationCacheImport{
		SchemaVersion: anyJevDistillationCacheSchema,
		ModelSHA256:   anyJevStringSHA256(model), QuestionSHA256: anyJevStringSHA256(question),
		Split: split, ClassCount: classCount, Block: block,
		OmittedFields:       []string{"per_example_probabilities", "gold_labels", "feature_vectors", "synthetic_states", "raw_model_identifiers", "raw_question_identifiers"},
		HumanReviewRequired: true, ExecutionVerified: false, CutoverEligible: false,
	}

	if strings.EqualFold(parts[2], "synth_labels") {
		if split == "synth" {
			projected.CacheKind = "teacher_probabilities"
			if len(arrays) != 3 || arrays["probs"] == nil || arrays["y_gold"] != nil {
				return normalizedItem{}, errors.New("synthetic label cache has an unexpected array set")
			}
		} else {
			projected.CacheKind = "teacher_test_labels"
			if len(arrays) != 4 || arrays["probs"] == nil || arrays["y_gold"] == nil {
				return normalizedItem{}, errors.New("test label cache has an unexpected array set")
			}
		}
		probs := arrays["probs"]
		if probs == nil || probs.kind != 'f' || len(probs.shape) != 2 || probs.shape[0] < 1 ||
			probs.shape[0] > 100000 || probs.shape[1] != classCount ||
			probs.shape[0]*probs.shape[1] > anyJevNPYMaxElements {
			return normalizedItem{}, errors.New("AnyJev distillation probability matrix shape is invalid")
		}
		n := probs.shape[0]
		projected.Examples = n
		if split == "test" {
			gold := arrays["y_gold"]
			if gold == nil || !anyJevNPYHasShape(gold, n) || (gold.kind != 'i' && gold.kind != 'u') {
				return normalizedItem{}, errors.New("AnyJev distillation gold-label vector shape is invalid")
			}
		}
		meanConfidence, meanEntropy, counts, accuracy, ece, nll, err := summarizeAnyJevProbabilities(probs, arrays["y_gold"], classCount)
		if err != nil {
			return normalizedItem{}, err
		}
		projected.MeanTopConfidence = &meanConfidence
		projected.MeanEntropy = &meanEntropy
		projected.ArgmaxCounts = counts
		if split == "test" {
			projected.Top1Accuracy = &accuracy
			projected.ExpectedCalibration = &ece
			projected.NegativeLogLoss = &nll
		}
	} else {
		projected.CacheKind = "student_features"
		features := arrays["H"]
		if len(arrays) != 3 || features == nil || features.kind != 'f' || len(features.shape) != 2 ||
			features.shape[0] < 1 || features.shape[0] > 100000 ||
			features.shape[1] < 1 || features.shape[1] > 65536 ||
			features.shape[0]*features.shape[1] > anyJevNPYMaxElements ||
			arrays["probs"] != nil || arrays["y_gold"] != nil {
			return normalizedItem{}, errors.New("AnyJev distillation feature matrix shape or members are invalid")
		}
		for index := 0; index < features.shape[0]*features.shape[1]; index++ {
			value := anyJevNPYFloatAt(features, index)
			if math.IsNaN(value) || math.IsInf(value, 0) {
				return normalizedItem{}, errors.New("AnyJev feature cache contains a non-finite value")
			}
		}
		projected.Examples = features.shape[0]
		projected.FeatureWidth = features.shape[1]
	}

	encoded, err := json.Marshal(projected)
	if err != nil {
		return normalizedItem{}, err
	}
	if privacy.ContainsCredential(encoded) {
		return normalizedItem{}, errors.New("AnyJev distillation cache projection contains credential-like data")
	}
	return normalizedItem{Type: "decision_label_or_evaluation", Payload: encoded}, nil
}

func readAnyJevNPZ(data []byte) (map[string]*anyJevNPYArray, error) {
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil || len(reader.File) < 3 || len(reader.File) > anyJevNPZMaxEntries {
		return nil, errors.New("AnyJev distillation cache is not a bounded NPZ archive")
	}
	arrays := make(map[string]*anyJevNPYArray, len(reader.File))
	var expanded int64
	for _, file := range reader.File {
		if file.FileInfo().IsDir() || path.Clean(file.Name) != file.Name || strings.Contains(file.Name, `\`) ||
			!strings.HasSuffix(file.Name, ".npy") || len(file.Name) <= len(".npy") {
			return nil, errors.New("AnyJev distillation cache contains an invalid archive entry")
		}
		name := strings.TrimSuffix(file.Name, ".npy")
		if name != "K" && name != "block" && name != "probs" && name != "y_gold" && name != "H" {
			return nil, errors.New("AnyJev distillation cache contains an unknown array")
		}
		if _, duplicate := arrays[name]; duplicate {
			return nil, errors.New("AnyJev distillation cache contains duplicate arrays")
		}
		if file.UncompressedSize64 > anyJevNPZMaxExpandedBytes || expanded > anyJevNPZMaxExpandedBytes-int64(file.UncompressedSize64) {
			return nil, errors.New("AnyJev distillation cache exceeds expanded-byte limit")
		}
		expanded += int64(file.UncompressedSize64)
		member, err := file.Open()
		if err != nil {
			return nil, errors.New("AnyJev distillation cache contains an unreadable array")
		}
		contents, readErr := io.ReadAll(io.LimitReader(member, int64(file.UncompressedSize64)+1))
		closeErr := member.Close()
		if readErr != nil || closeErr != nil || uint64(len(contents)) != file.UncompressedSize64 {
			return nil, errors.New("AnyJev distillation cache array size is invalid")
		}
		array, err := parseAnyJevNPY(contents)
		if err != nil {
			return nil, err
		}
		arrays[name] = array
	}
	if arrays["K"] == nil || arrays["block"] == nil {
		return nil, errors.New("AnyJev distillation cache omits K or block")
	}
	return arrays, nil
}

func parseAnyJevNPY(data []byte) (*anyJevNPYArray, error) {
	if len(data) < 10 || !bytes.Equal(data[:6], []byte{0x93, 'N', 'U', 'M', 'P', 'Y'}) {
		return nil, errors.New("AnyJev distillation cache contains an invalid NPY member")
	}
	major := data[6]
	var headerStart, headerLength int
	switch major {
	case 1:
		headerStart = 10
		headerLength = int(binary.LittleEndian.Uint16(data[8:10]))
	case 2, 3:
		if len(data) < 12 {
			return nil, errors.New("AnyJev distillation cache NPY header is truncated")
		}
		headerStart = 12
		length := binary.LittleEndian.Uint32(data[8:12])
		if length > anyJevNPYMaxHeaderBytes {
			return nil, errors.New("AnyJev distillation cache NPY header exceeds limit")
		}
		headerLength = int(length)
	default:
		return nil, errors.New("AnyJev distillation cache NPY version is unsupported")
	}
	if headerLength < 1 || headerLength > anyJevNPYMaxHeaderBytes || headerStart > len(data)-headerLength {
		return nil, errors.New("AnyJev distillation cache NPY header length is invalid")
	}
	headerEnd := headerStart + headerLength
	if headerEnd%16 != 0 || data[headerEnd-1] != '\n' {
		return nil, errors.New("AnyJev distillation cache NPY header framing is invalid")
	}
	header := string(data[headerStart:headerEnd])
	fields := anyJevNPYHeaderPattern.FindStringSubmatch(header)
	if len(fields) != 5 || fields[3] != "False" {
		return nil, errors.New("AnyJev distillation cache NPY descriptor is unsupported")
	}
	order := byte('<')
	if fields[1] != "" {
		order = fields[1][0]
	}
	if order == '=' {
		order = '<'
	}
	if order != '<' && order != '>' && order != '|' {
		return nil, errors.New("AnyJev distillation cache NPY byte order is unsupported")
	}
	typeName := fields[2]
	kind := typeName[0]
	if kind != 'f' && kind != 'i' && kind != 'u' {
		return nil, errors.New("AnyJev distillation cache NPY dtype is unsupported")
	}
	itemSize, err := strconv.Atoi(typeName[1:])
	if err != nil || (kind == 'f' && itemSize != 2 && itemSize != 4 && itemSize != 8) ||
		((kind == 'i' || kind == 'u') && itemSize != 1 && itemSize != 2 && itemSize != 4 && itemSize != 8) ||
		(order == '|' && itemSize != 1) {
		return nil, errors.New("AnyJev distillation cache NPY element width is unsupported")
	}
	shapeText := strings.TrimSpace(fields[4])
	shape := []int{}
	if shapeText != "" {
		for _, dimensionText := range strings.Split(strings.TrimSuffix(shapeText, ","), ",") {
			dimension, err := strconv.Atoi(strings.TrimSpace(dimensionText))
			if err != nil || dimension < 1 || len(shape) >= 2 {
				return nil, errors.New("AnyJev distillation cache NPY dimensions are invalid")
			}
			shape = append(shape, dimension)
		}
	}
	count := 1
	for _, dimension := range shape {
		if count > anyJevNPYMaxElements/dimension {
			return nil, errors.New("AnyJev distillation cache NPY array exceeds element limit")
		}
		count *= dimension
	}
	if count > anyJevNPYMaxElements || count > (len(data)-headerEnd)/itemSize ||
		len(data)-headerEnd != count*itemSize {
		return nil, errors.New("AnyJev distillation cache NPY payload size does not match its shape")
	}
	return &anyJevNPYArray{kind: kind, itemSize: itemSize, byteOrder: order, shape: shape, data: data[headerEnd:]}, nil
}

func anyJevNPYHasShape(array *anyJevNPYArray, dimensions ...int) bool {
	if array == nil || len(array.shape) != len(dimensions) {
		return false
	}
	for index, dimension := range dimensions {
		if array.shape[index] != dimension {
			return false
		}
	}
	return true
}

func anyJevNPYIntegerScalar(array *anyJevNPYArray) (int, bool) {
	if array == nil || (array.kind != 'i' && array.kind != 'u') ||
		(len(array.shape) != 0 && !anyJevNPYHasShape(array, 1)) {
		return 0, false
	}
	value, ok := anyJevNPYIntegerAt(array, 0)
	if !ok || int64(int(value)) != value {
		return 0, false
	}
	return int(value), true
}

func anyJevNPYIntegerAt(array *anyJevNPYArray, index int) (int64, bool) {
	if array == nil || array.itemSize < 1 || index < 0 || index >= len(array.data)/array.itemSize {
		return 0, false
	}
	data := array.data[index*array.itemSize : (index+1)*array.itemSize]
	var unsigned uint64
	if array.byteOrder == '>' {
		switch array.itemSize {
		case 1:
			unsigned = uint64(data[0])
		case 2:
			unsigned = uint64(binary.BigEndian.Uint16(data))
		case 4:
			unsigned = uint64(binary.BigEndian.Uint32(data))
		case 8:
			unsigned = binary.BigEndian.Uint64(data)
		}
	} else {
		switch array.itemSize {
		case 1:
			unsigned = uint64(data[0])
		case 2:
			unsigned = uint64(binary.LittleEndian.Uint16(data))
		case 4:
			unsigned = uint64(binary.LittleEndian.Uint32(data))
		case 8:
			unsigned = binary.LittleEndian.Uint64(data)
		}
	}
	if array.kind == 'u' {
		if unsigned > math.MaxInt64 {
			return 0, false
		}
		return int64(unsigned), true
	}
	switch array.itemSize {
	case 1:
		return int64(int8(unsigned)), true
	case 2:
		return int64(int16(unsigned)), true
	case 4:
		return int64(int32(unsigned)), true
	case 8:
		return int64(unsigned), true
	default:
		return 0, false
	}
}

func anyJevNPYFloatAt(array *anyJevNPYArray, index int) float64 {
	data := array.data[index*array.itemSize : (index+1)*array.itemSize]
	if array.byteOrder == '>' {
		switch array.itemSize {
		case 2:
			return float64(anyJevFloat16(binary.BigEndian.Uint16(data)))
		case 4:
			return float64(math.Float32frombits(binary.BigEndian.Uint32(data)))
		case 8:
			return math.Float64frombits(binary.BigEndian.Uint64(data))
		}
	} else {
		switch array.itemSize {
		case 2:
			return float64(anyJevFloat16(binary.LittleEndian.Uint16(data)))
		case 4:
			return float64(math.Float32frombits(binary.LittleEndian.Uint32(data)))
		case 8:
			return math.Float64frombits(binary.LittleEndian.Uint64(data))
		}
	}
	return math.NaN()
}

func anyJevFloat16(value uint16) float32 {
	sign := uint32(value&0x8000) << 16
	exponent := int((value >> 10) & 0x1f)
	mantissa := uint32(value & 0x03ff)
	var bits uint32
	switch exponent {
	case 0:
		if mantissa == 0 {
			bits = sign
		} else {
			exponent = 127 - 15 + 1
			for mantissa&0x0400 == 0 {
				mantissa <<= 1
				exponent--
			}
			mantissa &= 0x03ff
			bits = sign | uint32(exponent)<<23 | mantissa<<13
		}
	case 0x1f:
		bits = sign | 0x7f800000 | mantissa<<13
	default:
		bits = sign | uint32(exponent+127-15)<<23 | mantissa<<13
	}
	return math.Float32frombits(bits)
}

func summarizeAnyJevProbabilities(probs, gold *anyJevNPYArray, classCount int) (float64, float64, []int, float64, float64, float64, error) {
	n := probs.shape[0]
	counts := make([]int, classCount)
	binCount := make([]int, 15)
	binConfidence := make([]float64, 15)
	binAccuracy := make([]float64, 15)
	var confidenceTotal, entropyTotal, correctTotal, nllTotal float64
	for row := 0; row < n; row++ {
		var sum, entropy float64
		maximum, argmax := -1.0, -1
		for class := 0; class < classCount; class++ {
			value := anyJevNPYFloatAt(probs, row*classCount+class)
			if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 1 {
				return 0, 0, nil, 0, 0, 0, errors.New("AnyJev distillation probabilities contain an invalid value")
			}
			sum += value
			if value > 0 {
				entropy -= value * math.Log(value)
			}
			if value > maximum {
				maximum, argmax = value, class
			}
		}
		if math.Abs(sum-1) > 1e-3 || argmax < 0 {
			return 0, 0, nil, 0, 0, 0, errors.New("AnyJev distillation probability row is not normalized")
		}
		counts[argmax]++
		confidenceTotal += maximum
		entropyTotal += entropy
		if gold != nil {
			label, ok := anyJevNPYIntegerAt(gold, row)
			if !ok || label < 0 || label >= int64(classCount) {
				return 0, 0, nil, 0, 0, 0, errors.New("AnyJev distillation gold label is outside class range")
			}
			if int64(argmax) == label {
				correctTotal++
			}
			probability := anyJevNPYFloatAt(probs, row*classCount+int(label))
			nllTotal -= math.Log(math.Max(probability, 1e-15))
			bin := int(maximum * float64(len(binCount)))
			if bin >= len(binCount) {
				bin = len(binCount) - 1
			}
			binCount[bin]++
			binConfidence[bin] += maximum
			if int64(argmax) == label {
				binAccuracy[bin]++
			}
		}
	}
	meanConfidence := confidenceTotal / float64(n)
	meanEntropy := entropyTotal / float64(n)
	accuracy, ece, nll := 0.0, 0.0, 0.0
	if gold != nil {
		accuracy = correctTotal / float64(n)
		nll = nllTotal / float64(n)
		for bin, count := range binCount {
			if count == 0 {
				continue
			}
			ece += float64(count) / float64(n) * math.Abs(binAccuracy[bin]/float64(count)-binConfidence[bin]/float64(count))
		}
	}
	return meanConfidence, meanEntropy, counts, accuracy, ece, nll, nil
}
