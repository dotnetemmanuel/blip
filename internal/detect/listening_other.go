//go:build !linux

package detect

func (procSource) Listening() ([]Listener, error) {
	return nil, nil
}
