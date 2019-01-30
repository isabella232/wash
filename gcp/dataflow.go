package gcp

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/puppetlabs/wash/datastore"
	"github.com/puppetlabs/wash/plugin"
	dataflow "google.golang.org/api/dataflow/v1b3"
)

type dataflowJob struct {
	name    string
	id      string
	client  *dataflow.Service
	updated time.Time
	*service
}

// Constructs a dataflowJob from id, which combines name and job id.
func newDataflowJob(id string, client *dataflow.Service, svc *service) *dataflowJob {
	name, id := splitDataflowID(id)
	return &dataflowJob{name, id, client, time.Now(), svc}
}

// String returns a printable representation of the dataflow job.
func (cli *dataflowJob) String() string {
	return fmt.Sprintf("gcp/%v/dataflow/job/%v", cli.proj, cli.name)
}

// Returns the dataflow job name.
func (cli *dataflowJob) Name() string {
	return cli.name
}

// Attr returns attributes of the named resource.
func (cli *dataflowJob) Attr(ctx context.Context) (*plugin.Attributes, error) {
	if buf, ok := cli.reqs[cli.name]; ok {
		return &plugin.Attributes{Mtime: buf.LastUpdate(), Size: uint64(buf.Size()), Valid: validDuration}, nil
	}

	return &plugin.Attributes{Mtime: cli.updated, Valid: validDuration}, nil
}

// Xattr returns a map of extended attributes.
func (cli *dataflowJob) Xattr(ctx context.Context) (map[string][]byte, error) {
	data, err := datastore.CachedJSON(cli.cache, cli.String(), func() ([]byte, error) {
		projJobSvc := dataflow.NewProjectsJobsService(cli.client)
		job, err := projJobSvc.Get(cli.proj, cli.id).Do()
		if err != nil {
			return nil, err
		}
		return job.MarshalJSON()
	})
	if err != nil {
		return nil, err
	}
	return plugin.JSONToJSONMap(data)
}

type dataflowReader struct {
	*dataflow.ProjectsJobsMessagesListCall
	overflow []*dataflow.JobMessage
	eof      bool
}

func (rdr *dataflowReader) consume(p []byte) ([]byte, int) {
	consumed, n := 0, 0
	for _, msg := range rdr.overflow {
		msgLen := len(msg.Time) + len(msg.MessageImportance) + len(msg.MessageText) + 3
		if msgLen > len(p) {
			break
		}
		copy(p, msg.Time+" "+msg.MessageImportance+" "+msg.MessageText+"\n")
		p = p[msgLen:]
		n += msgLen
		consumed++
	}
	rdr.overflow = rdr.overflow[consumed:]
	return p, n
}

func (rdr *dataflowReader) Read(p []byte) (n int, err error) {
	// If there was data left over, consume it. If any remains after filling the buffer, return.
	var read int
	if len(rdr.overflow) > 0 {
		p, read = rdr.consume(p)
		n += read
		if len(rdr.overflow) > 0 {
			return
		}
	}

	// If EOF was reached on a previous call, return that. We only reach this point if all overflow
	// has been consumed. Includes the number of bytes processing remaining overflow.
	if rdr.eof {
		err = io.EOF
		return
	}

	// Keep reading pages from the API as needed to fill the buffer. Stash overflow.
	var resp *dataflow.ListJobMessagesResponse
	for {
		resp, err = rdr.Do()
		if err != nil {
			return
		}

		// Process response
		rdr.overflow = resp.JobMessages
		p, read = rdr.consume(p)
		n += read

		// Setup the next read. If NextPageToken was empty, mark EOF.
		rdr.PageToken(resp.NextPageToken)
		if resp.NextPageToken == "" {
			rdr.eof = true
		}

		// If the buffer is full or there's no more data to read, return.
		if len(rdr.overflow) > 0 || rdr.eof {
			return
		}
	}
}

func (rdr *dataflowReader) Close() error {
	return nil
}

func (cli *dataflowJob) readLog() (io.ReadCloser, error) {
	lister := dataflow.NewProjectsJobsMessagesService(cli.client).List(cli.proj, cli.id)
	return &dataflowReader{ProjectsJobsMessagesListCall: lister}, nil
}

// Open subscribes to a dataflow job and reads new messages.
func (cli *dataflowJob) Open(ctx context.Context) (plugin.IFileBuffer, error) {
	// TODO: this is pretty generic boilerplate
	cli.mux.Lock()
	defer cli.mux.Unlock()

	buf, ok := cli.reqs[cli.name]
	if !ok {
		buf = datastore.NewBuffer(cli.name, nil)
		cli.reqs[cli.name] = buf
	}

	buffered := make(chan bool)
	go func() {
		buf.Stream(cli.readLog, buffered)
	}()
	// Wait for some output to buffer.
	<-buffered

	return buf, nil
}

// Returns an array where every even entry is a job name and the following entry is its id.
func (cli *service) cachedDataflowJobs(c *dataflow.Service) ([]string, error) {
	return datastore.CachedStrings(cli.cache, cli.String(), func() ([]string, error) {
		projJobSvc := dataflow.NewProjectsJobsService(c)
		projJobsResp, err := projJobSvc.List(cli.proj).Do()
		if err != nil {
			return nil, err
		}

		jobs := make([]string, len(projJobsResp.Jobs))
		for i, job := range projJobsResp.Jobs {
			jobs[i] = job.Name + "/" + job.Id
		}
		cli.updated = time.Now()
		return jobs, nil
	})
}

func searchDataflowJob(jobs []string, name string) (string, bool) {
	idx := sort.Search(len(jobs), func(i int) bool {
		x, _ := splitDataflowID(jobs[i])
		return x >= name
	})
	if idx < len(jobs) {
		x, _ := splitDataflowID(jobs[idx])
		if x == name {
			return jobs[idx], true
		}
	}
	return "", false
}

func splitDataflowID(id string) (string, string) {
	// name is required to match [a-z]([-a-z0-9]{0,38}[a-z0-9])?, and id can additionally
	// include underscores. Use '/' as a separator.
	tokens := strings.Split(id, "/")
	if len(tokens) != 2 {
		panic("newDataflowJob given an invalid name/id pair")
	}
	return tokens[0], tokens[1]
}
