package cli

import (
	"github.com/spf13/cobra"

	"github.com/dotnetemmanuel/blip/internal/request"
)

const rawLong = `Send a request to a path, with no spec involved.

The environment's base URL, credentials, TLS settings and safety rules all apply,
so raw is the escape hatch for an API that has no spec, or an endpoint the spec
forgot. A path must start with /; blip will not send a request to another host.

Bodies come from --data (inline JSON, @file, or @- for stdin) or from repeated
--field pairs. A --field key=value is a string; key:=value takes raw JSON, which
is how to send a number, a boolean or null.`

func newRawCommand(rt *Runtime) *cobra.Command {
	var (
		data    string
		fields  []string
		queries []string
		headers []string
	)

	cmd := &cobra.Command{
		Use:   "raw <METHOD> <PATH>",
		Short: "Send a request without a spec",
		Long:  rawLong,
		Args:  usageArgs(cobra.ExactArgs(2)),
		Example: `  blip raw GET /healthz
  blip raw GET /api/orders --query status=open
  blip raw POST /api/orders --data @order.json --dry-run
  blip raw POST /api/orders --field sku=A1 --field qty:=3 --yes`,
		RunE: func(cmd *cobra.Command, args []string) error {
			c := call{Method: args[0], Path: args[1]}

			var err error
			if c.Query, err = request.ParseQuery(queries); err != nil {
				return err
			}
			if c.Header, err = request.ParseHeaders(headers); err != nil {
				return err
			}
			if c.Body, err = rt.body(data, fields); err != nil {
				return err
			}

			return rt.send(cmd.Context(), c)
		},
	}

	cmd.Flags().StringVar(&data, "data", "", "request body: inline, @file, or @- for stdin")
	cmd.Flags().StringArrayVar(&fields, "field", nil, "body field key=value, or key:=value for raw JSON (repeatable)")
	cmd.Flags().StringArrayVar(&queries, "query", nil, "query parameter name=value (repeatable)")
	cmd.Flags().StringArrayVar(&headers, "header", nil, "request header \"Name: value\" (repeatable)")

	return cmd
}

// body resolves --data and --field, which are alternatives rather than a pair.
func (rt *Runtime) body(data string, fields []string) ([]byte, error) {
	switch {
	case data != "" && len(fields) > 0:
		return nil, usageError("--data and --field cannot be combined; --field is for flat objects, --data for everything else")
	case data != "":
		return request.Data(data, rt.input())
	case len(fields) > 0:
		return request.Fields(fields)
	default:
		return nil, nil
	}
}
