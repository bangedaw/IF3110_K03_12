package queue

import "context"

// EncodeTrace dan WithTrace membuka encode/decode trace context milik Publish dan Claim untuk test, supaya
// rantai trace bisa diuji tanpa platform DB.
func EncodeTrace(ctx context.Context) ([]byte, error) { return encodeTrace(ctx) }

func WithTrace(j *Job, raw []byte) error {
	carrier, err := decodeTrace(raw)
	j.carrier = carrier
	return err
}
