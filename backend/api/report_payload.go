package api

import (
	"encoding"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/network-troubleshooting-company/checknetwork/backend/diagnostic"
)

const (
	maxFullResponseBytes             = 8 << 20
	maxFullResponseDepth             = 64
	maxFullResponseContainers        = 32_768
	maxFullResponseNodes             = 262_144
	maxFullResponseStringBytes       = 1_000_000
	maxFullResponseTopologyNodes     = 8_192
	maxFullResponseTopologyLinks     = 16_384
	maxFullResponseContainerElements = 32_768
)

var (
	errFullResponseTooLarge       = errors.New("full response too large")
	errFullResponseUnsupported    = errors.New("full response contains unsupported value")
	errFullResponseCycle          = errors.New("full response contains cycle")
	errFullResponseDepthLimit     = errors.New("full response exceeds depth limit")
	errFullResponseContainerLimit = errors.New("full response exceeds container limit")
	errFullResponseNodeLimit      = errors.New("full response exceeds node limit")
	errFullResponseStringLimit    = errors.New("full response exceeds string limit")
	errFullResponseNonFinite      = errors.New("full response contains non-finite number")
)

var (
	jsonMarshalerType      = reflect.TypeOf((*json.Marshaler)(nil)).Elem()
	textMarshalerType      = reflect.TypeOf((*encoding.TextMarshaler)(nil)).Elem()
	timeType               = reflect.TypeOf(time.Time{})
	reportType             = reflect.TypeOf(diagnostic.Report{})
	enrichmentCoverageType = reflect.TypeOf(diagnostic.EnrichmentCoverage{})
	enrichmentFailureType  = reflect.TypeOf(diagnostic.EnrichmentFailure{})
)

// marshalFullResponse validates the report into a closed transport domain before
// allocating or encoding its response body. It intentionally never passes a
// report, struct, map, slice, or array to encoding/json.
func marshalFullResponse(report diagnostic.Report) ([]byte, error) {
	return marshalClosedResponse(report, maxFullResponseBytes, true)
}

// marshalClosedResponse validates a Report into the transport's closed value
// domain, then incrementally encodes it without ever handing a whole composite
// value to encoding/json. outputCap is a hard bound on retained output bytes.
func marshalClosedResponse(report diagnostic.Report, outputCap int, newline bool) ([]byte, error) {
	validator := fullResponseValidator{
		active:     make(map[fullResponseIdentity]struct{}),
		stringKeys: make(map[string]struct{}),
	}
	if err := validator.validate(reflect.ValueOf(report), 0, false); err != nil {
		return nil, err
	}

	buffer := newLimitedJSONBuffer(outputCap)
	encoder := fullResponseEncoder{buffer: buffer}
	if err := encoder.encode(reflect.ValueOf(report), false); err != nil {
		return nil, err
	}
	if newline {
		if err := buffer.writeByte('\n'); err != nil {
			return nil, err
		}
	}
	return buffer.bytes(), nil
}

type fullResponseIdentity struct {
	kind  reflect.Kind
	type_ reflect.Type
	ptr   uintptr
	len   int
}

type fullResponseValidator struct {
	active        map[fullResponseIdentity]struct{}
	stringKeys    map[string]struct{}
	containers    int
	nodes         int
	stringBytes   int
	topologyNodes int
	topologyLinks int
}

func (v *fullResponseValidator) validate(value reflect.Value, depth int, dynamic bool) error {
	for value.IsValid() && value.Kind() == reflect.Interface {
		if value.IsNil() {
			return v.addNode()
		}
		value = value.Elem()
	}
	if !value.IsValid() {
		return v.addNode()
	}
	if err := v.addNode(); err != nil {
		return err
	}
	if depth > maxFullResponseDepth {
		return errFullResponseDepthLimit
	}

	typeOfValue := value.Type()
	if typeOfValue != timeType && implementsCustomMarshaler(typeOfValue) {
		return errFullResponseUnsupported
	}

	switch value.Kind() {
	case reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return nil
	case reflect.Float32, reflect.Float64:
		if math.IsNaN(value.Float()) || math.IsInf(value.Float(), 0) {
			return errFullResponseNonFinite
		}
		return nil
	case reflect.String:
		return v.addString(value.Len())
	case reflect.Pointer:
		if value.IsNil() {
			return nil
		}
		if !allowedPointerTarget(typeOfValue.Elem(), dynamic) {
			return errFullResponseUnsupported
		}
		identity := fullResponseIdentity{kind: value.Kind(), type_: typeOfValue, ptr: value.Pointer()}
		return v.withActive(identity, func() error {
			return v.validate(value.Elem(), depth+1, dynamic)
		})
	case reflect.Struct:
		if typeOfValue == timeType {
			return nil
		}
		if !allowedStruct(typeOfValue, dynamic) {
			return errFullResponseUnsupported
		}
		if typeOfValue.Name() == "TopologyNode" || typeOfValue.Name() == "CompactTopologyNode" {
			v.topologyNodes++
			if v.topologyNodes > maxFullResponseTopologyNodes {
				return errFullResponseNodeLimit
			}
		}
		if typeOfValue.Name() == "TopologyLink" || typeOfValue.Name() == "CompactTopologyLink" {
			v.topologyLinks++
			if v.topologyLinks > maxFullResponseTopologyLinks {
				return errFullResponseNodeLimit
			}
		}
		if err := v.addContainer(typeOfValue.NumField()); err != nil {
			return err
		}
		for fieldIndex := 0; fieldIndex < typeOfValue.NumField(); fieldIndex++ {
			fieldInfo := typeOfValue.Field(fieldIndex)
			if !fieldInfo.IsExported() {
				continue
			}
			_, options, skip := jsonField(fieldInfo)
			if skip {
				continue
			}
			field := value.Field(fieldIndex)
			if options.Contains("omitempty") && isJSONEmpty(field) {
				continue
			}
			childDynamic := dynamicDetailsField(typeOfValue, fieldInfo)
			if err := v.validate(field, depth+1, childDynamic); err != nil {
				return err
			}
		}
		return nil
	case reflect.Map:
		if value.IsNil() {
			return nil
		}
		if typeOfValue.Key().Kind() != reflect.String {
			return errFullResponseUnsupported
		}
		if value.Len() > maxFullResponseContainerElements {
			return errFullResponseContainerLimit
		}
		if implementsCustomMarshaler(typeOfValue.Key()) {
			return errFullResponseUnsupported
		}
		if err := v.addContainer(value.Len()); err != nil {
			return err
		}
		identity := fullResponseIdentity{kind: value.Kind(), type_: typeOfValue, ptr: value.Pointer()}
		return v.withActive(identity, func() error {
			iterator := value.MapRange()
			for iterator.Next() {
				if err := v.addStringKey(iterator.Key().String()); err != nil {
					return err
				}
				if err := v.validate(iterator.Value(), depth+1, dynamic); err != nil {
					return err
				}
			}
			return nil
		})
	case reflect.Slice:
		if value.IsNil() {
			return nil
		}
		if value.Len() > maxFullResponseContainerElements {
			return errFullResponseContainerLimit
		}
		if err := v.addContainer(value.Len()); err != nil {
			return err
		}
		if value.Len() == 0 {
			return nil
		}
		identity := fullResponseIdentity{kind: value.Kind(), type_: typeOfValue, ptr: value.Pointer(), len: value.Len()}
		return v.withActive(identity, func() error {
			for index := 0; index < value.Len(); index++ {
				if err := v.validate(value.Index(index), depth+1, dynamic); err != nil {
					return err
				}
			}
			return nil
		})
	case reflect.Array:
		if value.Len() > maxFullResponseContainerElements {
			return errFullResponseContainerLimit
		}
		if err := v.addContainer(value.Len()); err != nil {
			return err
		}
		for index := 0; index < value.Len(); index++ {
			if err := v.validate(value.Index(index), depth+1, dynamic); err != nil {
				return err
			}
		}
		return nil
	default:
		return errFullResponseUnsupported
	}
}

func (v *fullResponseValidator) addNode() error {
	v.nodes++
	if v.nodes > maxFullResponseNodes {
		return errFullResponseNodeLimit
	}
	return nil
}

func (v *fullResponseValidator) addContainer(elements int) error {
	if elements > maxFullResponseContainerElements {
		return errFullResponseContainerLimit
	}
	v.containers++
	if v.containers > maxFullResponseContainers {
		return errFullResponseContainerLimit
	}
	return nil
}

func (v *fullResponseValidator) addString(length int) error {
	if length > maxFullResponseStringBytes || v.stringBytes > maxFullResponseStringBytes-length {
		return errFullResponseStringLimit
	}
	v.stringBytes += length
	return nil
}

func (v *fullResponseValidator) addStringKey(key string) error {
	if _, exists := v.stringKeys[key]; exists {
		return nil
	}
	if err := v.addString(len(key)); err != nil {
		return err
	}
	v.stringKeys[key] = struct{}{}
	return nil
}

func (v *fullResponseValidator) withActive(identity fullResponseIdentity, visit func() error) error {
	if _, exists := v.active[identity]; exists {
		return errFullResponseCycle
	}
	v.active[identity] = struct{}{}
	defer delete(v.active, identity)
	return visit()
}

func implementsJSONMarshaler(valueType reflect.Type) bool {
	return implementsInterface(valueType, jsonMarshalerType)
}

func implementsCustomMarshaler(valueType reflect.Type) bool {
	return implementsJSONMarshaler(valueType) || implementsInterface(valueType, textMarshalerType)
}

func implementsInterface(valueType, interfaceType reflect.Type) bool {
	if valueType.Implements(interfaceType) {
		return true
	}
	return valueType.Kind() != reflect.Pointer && reflect.PointerTo(valueType).Implements(interfaceType)
}

func allowedPointerTarget(target reflect.Type, dynamic bool) bool {
	if target == timeType || allowedStruct(target, dynamic) {
		return true
	}
	if dynamic {
		return false
	}
	switch target.Kind() {
	case reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64, reflect.String:
		return true
	default:
		return false
	}
}

func allowedStruct(valueType reflect.Type, dynamic bool) bool {
	if valueType == reportType {
		return !dynamic
	}
	if valueType == enrichmentCoverageType || valueType == enrichmentFailureType {
		return true
	}
	if valueType.PkgPath() != reportType.PkgPath() {
		return false
	}
	if dynamic {
		switch valueType.Name() {
		case "TraceAttempt", "Topology", "TopologyNode", "TopologyLink", "GeoLocation", "ASNInfo", "IPMetadata":
			return true
		default:
			return false
		}
	}
	switch valueType.Name() {
	case "Result", "Summary", "Analysis", "Finding", "Evidence", "Action", "CoverageIssue", "Coverage",
		"TraceAttempt", "Topology", "TopologyNode", "TopologyLink", "GeoLocation", "ASNInfo", "IPMetadata",
		"CompactTopology", "CompactTopologyLimits", "CompactTopologyNode", "CompactTopologyLink", "CompactTopologyRoute",
		"CompactCountStats", "CompactRouteStats", "CompactTopologyStats", "CompactResultStats", "CompactGeoStats":
		return true
	default:
		return false
	}
}

func dynamicDetailsField(owner reflect.Type, field reflect.StructField) bool {
	return owner.Name() == "Result" && owner.PkgPath() == reportType.PkgPath() && field.Name == "Details"
}

type jsonTagOptions string

func (options jsonTagOptions) Contains(option string) bool {
	for options != "" {
		var current string
		if comma := strings.IndexByte(string(options), ','); comma >= 0 {
			current, options = string(options[:comma]), options[comma+1:]
		} else {
			current, options = string(options), ""
		}
		if current == option {
			return true
		}
	}
	return false
}

func jsonField(field reflect.StructField) (string, jsonTagOptions, bool) {
	tag := field.Tag.Get("json")
	if tag == "-" {
		return "", "", true
	}
	name, options, _ := strings.Cut(tag, ",")
	if name == "" {
		name = field.Name
	}
	return name, jsonTagOptions(options), false
}

func isJSONEmpty(value reflect.Value) bool {
	switch value.Kind() {
	case reflect.Array, reflect.Map, reflect.Slice, reflect.String:
		return value.Len() == 0
	case reflect.Bool:
		return !value.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return value.Int() == 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return value.Uint() == 0
	case reflect.Float32, reflect.Float64:
		return value.Float() == 0
	case reflect.Interface, reflect.Pointer:
		return value.IsNil()
	}
	return false
}

type fullResponseEncoder struct {
	buffer *limitedJSONBuffer
}

func (e *fullResponseEncoder) encode(value reflect.Value, dynamic bool) error {
	for value.IsValid() && value.Kind() == reflect.Interface {
		if value.IsNil() {
			return e.buffer.writeString("null")
		}
		value = value.Elem()
	}
	if !value.IsValid() {
		return e.buffer.writeString("null")
	}
	if value.Type() == timeType {
		return e.encodeLeaf(value.Interface())
	}

	switch value.Kind() {
	case reflect.Bool:
		return e.buffer.writeString(strconv.FormatBool(value.Bool()))
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return e.buffer.writeString(strconv.FormatInt(value.Int(), 10))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return e.buffer.writeString(strconv.FormatUint(value.Uint(), 10))
	case reflect.Float32, reflect.Float64:
		return e.encodeLeaf(value.Interface())
	case reflect.String:
		return e.encodeLeaf(value.String())
	case reflect.Pointer:
		if value.IsNil() {
			return e.buffer.writeString("null")
		}
		return e.encode(value.Elem(), dynamic)
	case reflect.Struct:
		if err := e.buffer.writeByte('{'); err != nil {
			return err
		}
		wrote := false
		valueType := value.Type()
		for fieldIndex := 0; fieldIndex < valueType.NumField(); fieldIndex++ {
			fieldInfo := valueType.Field(fieldIndex)
			if !fieldInfo.IsExported() {
				continue
			}
			name, options, skip := jsonField(fieldInfo)
			if skip {
				continue
			}
			field := value.Field(fieldIndex)
			if options.Contains("omitempty") && isJSONEmpty(field) {
				continue
			}
			if wrote {
				if err := e.buffer.writeByte(','); err != nil {
					return err
				}
			}
			wrote = true
			if err := e.encodeLeaf(name); err != nil {
				return err
			}
			if err := e.buffer.writeByte(':'); err != nil {
				return err
			}
			if err := e.encode(field, dynamicDetailsField(valueType, fieldInfo)); err != nil {
				return err
			}
		}
		return e.buffer.writeByte('}')
	case reflect.Map:
		if value.IsNil() {
			return e.buffer.writeString("null")
		}
		keys := value.MapKeys()
		sort.Slice(keys, func(i, j int) bool { return keys[i].String() < keys[j].String() })
		if err := e.buffer.writeByte('{'); err != nil {
			return err
		}
		for index, key := range keys {
			if index > 0 {
				if err := e.buffer.writeByte(','); err != nil {
					return err
				}
			}
			if err := e.encodeLeaf(key.String()); err != nil {
				return err
			}
			if err := e.buffer.writeByte(':'); err != nil {
				return err
			}
			if err := e.encode(value.MapIndex(key), dynamic); err != nil {
				return err
			}
		}
		return e.buffer.writeByte('}')
	case reflect.Slice:
		if value.IsNil() {
			return e.buffer.writeString("null")
		}
		if value.Type().Elem().Kind() == reflect.Uint8 {
			return e.encodeByteSlice(value)
		}
		fallthrough
	case reflect.Array:
		if err := e.buffer.writeByte('['); err != nil {
			return err
		}
		for index := 0; index < value.Len(); index++ {
			if index > 0 {
				if err := e.buffer.writeByte(','); err != nil {
					return err
				}
			}
			if err := e.encode(value.Index(index), dynamic); err != nil {
				return err
			}
		}
		return e.buffer.writeByte(']')
	default:
		return errFullResponseUnsupported
	}
}

func (e *fullResponseEncoder) encodeLeaf(value any) error {
	token, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("%w: %v", errResponseSerialization, err)
	}
	return e.buffer.writeBytes(token)
}

func (e *fullResponseEncoder) encodeByteSlice(value reflect.Value) error {
	if err := e.buffer.writeByte('"'); err != nil {
		return err
	}
	var source [3]byte
	var encoded [4]byte
	for offset := 0; offset < value.Len(); offset += len(source) {
		count := min(len(source), value.Len()-offset)
		for index := 0; index < count; index++ {
			source[index] = byte(value.Index(offset + index).Uint())
		}
		base64.StdEncoding.Encode(encoded[:], source[:count])
		if err := e.buffer.writeBytes(encoded[:]); err != nil {
			return err
		}
	}
	return e.buffer.writeByte('"')
}

type limitedJSONBuffer struct {
	data  []byte
	limit int
	err   error
}

func newLimitedJSONBuffer(limit int) *limitedJSONBuffer {
	initial := min(limit, 4096)
	return &limitedJSONBuffer{data: make([]byte, 0, initial), limit: limit}
}

func (b *limitedJSONBuffer) bytes() []byte { return b.data }

func (b *limitedJSONBuffer) writeByte(value byte) error {
	if err := b.reserve(1); err != nil {
		return err
	}
	b.data = append(b.data, value)
	return nil
}

func (b *limitedJSONBuffer) writeString(value string) error {
	if err := b.reserve(len(value)); err != nil {
		return err
	}
	b.data = append(b.data, value...)
	return nil
}

func (b *limitedJSONBuffer) writeBytes(value []byte) error {
	if err := b.reserve(len(value)); err != nil {
		return err
	}
	b.data = append(b.data, value...)
	return nil
}

func (b *limitedJSONBuffer) reserve(additional int) error {
	if b.err != nil {
		return b.err
	}
	if additional < 0 || len(b.data) > b.limit-additional {
		b.err = errFullResponseTooLarge
		return b.err
	}
	required := len(b.data) + additional
	if required <= cap(b.data) {
		return nil
	}
	capacity := cap(b.data) * 2
	if capacity < required {
		capacity = required
	}
	if capacity > b.limit {
		capacity = b.limit
	}
	grown := make([]byte, len(b.data), capacity)
	copy(grown, b.data)
	b.data = grown
	return nil
}
