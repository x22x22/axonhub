package llm

import (
	"bytes"
	"encoding/json"
)

// SystemOneRequest represents a System One decision inference request.
type SystemOneRequest struct {
	// State is the context or entity to evaluate (can be string, map, or slice).
	State any `json:"state"`

	// Questions is the map of question identifiers to their schema definitions.
	Questions map[string]SystemOneQuestion `json:"questions"`

	// Assets holds media referenced by placeholder from State, Instructions or
	// Criteria. Each key is an opaque placeholder (a per-image UUID is a good
	// choice); every occurrence of that placeholder inside a string is replaced
	// with the asset before the model sees it. Providers gate image input behind
	// their own opt-in, which the outbound transformer enables automatically when
	// assets are present.
	Assets map[string]SystemOneAsset `json:"assets,omitempty"`
}

// SystemOneAsset is a media attachment addressed by placeholder.
type SystemOneAsset struct {
	// Type identifies the asset kind. "image" is currently the only value.
	Type string `json:"type"`

	// Format is the image format (png, jpg or jpeg) and is required alongside
	// DataBase64.
	Format string `json:"format,omitempty"`

	// DataBase64 carries the asset bytes, base64-encoded.
	DataBase64 string `json:"data_base64,omitempty"`

	// Part names the multipart form part holding the bytes. It is only valid for
	// multipart requests, which AxonHub does not send, so the typesafe outbound
	// transformer rejects any asset that sets it. DataBase64 is the supported
	// path.
	Part string `json:"part,omitempty"`
}

// SystemOneQuestion defines a single decision question schema.
type SystemOneQuestion struct {
	// Type specifies the primitive type: "choice", "noul", "score", or "box".
	// "box" locates a region inside an image and requires image assets.
	Type string `json:"type"`

	// Instructions guides the decision model on how to evaluate this question (string, object, or array).
	Instructions any `json:"instructions"`

	// Criteria holds optional evaluation criteria/rubric.
	Criteria any `json:"criteria,omitempty"`

	// Extra carries question fields AxonHub does not model yet, so a provider
	// adding a new parameter to a primitive does not lose it in transit.
	Extra map[string]json.RawMessage `json:"-"`
}

func (q *SystemOneQuestion) UnmarshalJSON(data []byte) error {
	type Alias SystemOneQuestion
	var aux Alias
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	*q = SystemOneQuestion(aux)

	var rawMap map[string]json.RawMessage
	if err := json.Unmarshal(data, &rawMap); err != nil {
		return err
	}

	delete(rawMap, "type")
	delete(rawMap, "instructions")
	delete(rawMap, "criteria")

	if len(rawMap) > 0 {
		q.Extra = rawMap
	}
	return nil
}

func (q SystemOneQuestion) MarshalJSON() ([]byte, error) {
	type Alias SystemOneQuestion
	base, err := json.Marshal(Alias(q))
	if err != nil {
		return nil, err
	}
	if len(q.Extra) == 0 {
		return base, nil
	}

	var rawMap map[string]json.RawMessage
	if err := json.Unmarshal(base, &rawMap); err != nil {
		return nil, err
	}
	if rawMap == nil {
		rawMap = make(map[string]json.RawMessage)
	}
	for k, v := range q.Extra {
		rawMap[k] = v
	}
	return json.Marshal(rawMap)
}

// SystemOneResponse represents the unified result of a System One decision inference.
type SystemOneResponse struct {
	// Answers maps question identifiers to their evaluated results.
	Answers map[string]SystemOneAnswer `json:"answers"`
}

// SystemOneBox is a normalized bounding box, with each edge expressed as a
// fraction of the image dimension in the 0..1 range.
type SystemOneBox struct {
	Top    float64 `json:"top"`
	Bottom float64 `json:"bottom"`
	Left   float64 `json:"left"`
	Right  float64 `json:"right"`
}

// SystemOneAnswer holds the evaluated answer for a specific question.
type SystemOneAnswer struct {
	Type          string                     `json:"type,omitempty"`
	Choice        string                     `json:"choice,omitempty"`
	Noul          *float64                   `json:"noul,omitempty"`
	Score         *float64                   `json:"score,omitempty"`
	Box           *SystemOneBox              `json:"box,omitempty"`
	Legend        any                        `json:"legend,omitempty"`
	Probabilities any                        `json:"probabilities,omitempty"`
	Confidence    *float64                   `json:"confidence,omitempty"`
	Extra         map[string]json.RawMessage `json:"-"`
}

func (a *SystemOneAnswer) UnmarshalJSON(data []byte) error {
	type Alias SystemOneAnswer
	var aux Alias
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(&aux); err != nil {
		return err
	}
	*a = SystemOneAnswer(aux)

	var rawMap map[string]json.RawMessage
	if err := json.Unmarshal(data, &rawMap); err != nil {
		return err
	}

	delete(rawMap, "type")
	delete(rawMap, "choice")
	delete(rawMap, "noul")
	delete(rawMap, "score")
	delete(rawMap, "box")
	delete(rawMap, "legend")
	delete(rawMap, "probabilities")
	delete(rawMap, "confidence")

	if len(rawMap) > 0 {
		a.Extra = rawMap
	}
	return nil
}

func (a SystemOneAnswer) MarshalJSON() ([]byte, error) {
	type Alias SystemOneAnswer
	base, err := json.Marshal(Alias(a))
	if err != nil {
		return nil, err
	}
	if len(a.Extra) == 0 {
		return base, nil
	}

	var rawMap map[string]json.RawMessage
	if err := json.Unmarshal(base, &rawMap); err != nil {
		return nil, err
	}
	if rawMap == nil {
		rawMap = make(map[string]json.RawMessage)
	}
	for k, v := range a.Extra {
		rawMap[k] = v
	}
	return json.Marshal(rawMap)
}
