package compile

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"

	"github.com/dave/jsgo/builder"
	"github.com/dave/jsgo/builder/std"
	"github.com/dave/jsgo/config"
	"github.com/dave/jsgo/server/messages"
	"github.com/gopherjs/gopherjs/compiler"
)

func (c *Compiler) Update(ctx context.Context, info messages.Update, log io.Writer, source map[string]bool) error {

	c.send(messages.Updating{Starting: true})

	session := builder.NewSession(c.defaultOptions(log, false, source))

	index := messages.Index{}
	done := map[string]bool{}

	session.Callback = func(archive *compiler.Archive) error {

		if done[archive.ImportPath] {
			return nil
		}

		done[archive.ImportPath] = true

		if archive.ImportPath == "main" {
			return nil
		}

		if source[archive.ImportPath] {
			// don't return anything if the package is in the source collection
			return nil
		}

		// The archive files aren't binary identical across compiles, so we have to render them to JS
		// in order to get the hash for the cache. Not ideal, but it should work.
		_, hashBytes, err := builder.GetPackageCode(ctx, archive, false, true)
		if err != nil {
			return err
		}
		hash := fmt.Sprintf("%x", hashBytes)

		var unchanged bool
		if cached, exists := info.Cache[archive.ImportPath]; exists && cached == hash {
			unchanged = true
		}

		index[archive.ImportPath] = messages.IndexItem{
			Hash:      hash,
			Unchanged: unchanged,
		}

		if unchanged {
			// If the dependency is unchanged from the client cache, don't return it as a PlaygroundArchive
			// message
			return nil
		}

		if !config.UseLocal {
			if hashPair := std.Index[archive.ImportPath]; hashPair != nil {
				// All standard library archives are in the CDN, so we instruct the client to get them from
				// there. This way we can benefit from browser caching.
				c.send(messages.Archive{
					Path:     archive.ImportPath,
					Hash:     hashPair[false],
					Contents: nil,
					Standard: true,
				})
				return nil
			}
		}

		buf := &bytes.Buffer{}

		zw := gzip.NewWriter(buf)

		if err := compiler.WriteArchive(archive, zw); err != nil {
			return err
		}

		zw.Close()

		c.send(messages.Archive{
			Path:     archive.ImportPath,
			Hash:     hash,
			Contents: buf.Bytes(),
			Standard: false,
		})

		return nil
	}

	// All programs need runtime and it's dependencies
	if _, _, err := session.BuildImportPath(ctx, "runtime"); err != nil {
		return err
	}

	if _, _, err := session.BuildImportPath(ctx, "main"); err != nil {
		return err
	}

	c.send(index)

	c.send(messages.Updating{Done: true})

	return nil
}
