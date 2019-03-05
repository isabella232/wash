package api

import (
	"context"
	"fmt"
	"io"
	"net/http"

	"github.com/gorilla/mux"
	apitypes "github.com/puppetlabs/wash/api/types"
	"github.com/puppetlabs/wash/journal"
	"github.com/puppetlabs/wash/plugin"

	log "github.com/sirupsen/logrus"
)

var streamHandler handler = func(w http.ResponseWriter, r *http.Request) *errorResponse {
	if r.Method != http.MethodGet {
		return httpMethodNotSupported(r.Method, r.URL.Path, []string{http.MethodGet})
	}

	path := mux.Vars(r)["path"]
	log.Infof("API: Stream %v", path)

	jnl := journal.NamedJournal{ID: r.FormValue(apitypes.JournalID)}
	ctx := context.WithValue(r.Context(), plugin.Journal, jnl)

	entry, errResp := getEntryFromPath(ctx, path)
	if errResp != nil {
		return errResp
	}

	if !plugin.StreamAction.IsSupportedOn(entry) {
		return unsupportedActionResponse(path, plugin.StreamAction)
	}

	f, ok := w.(flushableWriter)
	if !ok {
		return unknownErrorResponse(fmt.Errorf("Cannot stream %v, response handler does not support flushing", path))
	}

	jnl.Log("API: Stream %v", path)
	rdr, err := entry.(plugin.Pipe).Stream(ctx)

	if err != nil {
		jnl.Log("API: Stream %v errored: %v", path, err)
		return erroredActionResponse(path, plugin.StreamAction, err.Error())
	}
	jnl.Log("API: Streaming %v", path)

	w.WriteHeader(http.StatusOK)
	// Ensure every write is a flush, and do an initial flush to send the header.
	wf := &streamableResponseWriter{f}
	f.Flush()

	if closer, ok := rdr.(io.Closer); ok {
		// If a ReadCloser, ensure it's closed when the context is cancelled.
		go func() {
			<-r.Context().Done()
			jnl.Log("API: Stream %v closed by completed context: %v", path, closer.Close())
		}()
	}
	if _, err := io.Copy(wf, rdr); err != nil {
		// Common for copy to error when the caller closes the connection.
		log.Debugf("Errored streaming response for entry %v: %v", path, err)
		jnl.Log("API: Streaming %v errored: %v", path, err)
	}

	jnl.Log("API: Streaming %v complete", path)
	return nil
}
