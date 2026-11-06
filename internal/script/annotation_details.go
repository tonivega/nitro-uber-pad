package script

// Annotation describes a session-local MSB-first field in original coordinates.
// Value is a live display string; scripts can read_range/read_bytes for arithmetic.
type Annotation struct {
 Start,Length int64
 Color,Label,Meaning,Possible,Value string
}

type annotationBatchHost interface {
 SetMarks([]Annotation) error
}

type annotationHost interface {
 SetMarkDetails(meaning,possible string) error
 AnnotationInfo() []Annotation
}
