package plugin

import (
	"fmt"
	"reflect"

	"github.com/ekinanp/jsonschema"
	"github.com/emirpasic/gods/maps/linkedhashmap"
)

// Schema returns the entry's schema. It is needed by the API,
// so plugin authors should ignore this.
func Schema(e Entry) (*EntrySchema, error) {
	if externalPlugin, ok := e.(externalPlugin); ok {
		return externalPlugin.schema()
	}
	return e.Schema(), nil
}

type entrySchema struct {
	// EntrySchemas are marshalled as JSON objects with key
	// <type_id> => <schema>. Thus, there's no need to include
	// the type_id more than once.
	TypeID              string      `json:"-"`
	Label               string      `json:"label"`
	Singleton           bool        `json:"singleton"`
	Actions             []string    `json:"actions"`
	MetaAttributeSchema *JSONSchema `json:"meta_attribute_schema"`
	MetadataSchema      *JSONSchema `json:"metadata_schema"`
	Children            []string    `json:"children"`
}

// EntrySchema represents an entry's schema. Use plugin.NewEntrySchema
// to create instances of these objects.
//
// EntrySchema's a useful way to document your plugin's hierarchy. Users
// can view your hierarchy via the stree command. For example, if you
// invoke `stree docker` in a Wash shell (try it!), you should see something
// like
//
// docker
// ├── containers
// │   └── [container]
// │       ├── log
// │       ├── metadata.json
// │       └── fs
// │           ├── [dir]
// │           │   ├── [dir]
// │           │   └── [file]
// │           └── [file]
// └── volumes
//     └── [volume]
//         ├── [dir]
//         │   ├── [dir]
//         │   └── [file]
// 		└── [file]
//
// (Your output may differ depending on the state of the Wash project, but it
// should be similarly structured).
//
// Every node must have a label. The "[]" are printed for non-singleton nodes;
// they imply multiple instances of this thing. For example, "[container]" means
// that there will be multiple "container" instances under the "containers" directory
// ("container" is the label that was passed into NewEntrySchema). Similarly, "containers"
// means that there will be only one "containers" directory (i.e. that "containers" is a
// singleton). You can use EntrySchema#IsSingleton() to mark your entry as a singleton.
//
// TODO: Talk about how metadata schema's used to optimize `wash find` once that
// is added.
type EntrySchema struct {
	// This pattern's a nice way of making JSON marshalling/unmarshalling
	// easy without having to export these fields via the godocs. The latter
	// is good because plugin authors should use the builders when setting them
	// (so that we maintain a consistent API for e.g. metadata schemas).
	//
	// This pattern was obtained from https://stackoverflow.com/a/11129474
	entrySchema
	metaAttributeSchemaObj interface{}
	metadataSchemaObj      interface{}
	entry                  Entry
	// graph is set by external plugins
	graph *linkedhashmap.Map
}

// NewEntrySchema returns a new EntrySchema object with the specified label.
//
// NOTE: If your entry's a singleton, then the label should match the entry's
// name, i.e. the name that's passed into plugin.NewEntry.
func NewEntrySchema(e Entry, label string) *EntrySchema {
	if len(label) == 0 {
		panic("plugin.NewEntrySchema called with an empty label")
	}
	t := unravelPtr(reflect.TypeOf(e))
	s := &EntrySchema{
		entrySchema: entrySchema{
			TypeID:  t.PkgPath() + "/" + t.Name(),
			Label:   label,
			Actions: SupportedActionsOf(e),
		},
		// The meta attribute's empty by default
		metaAttributeSchemaObj: struct{}{},
		// Store the entry so that when marshalling, we can enumerate
		// its child schemas (for core plugins only)
		entry: e,
	}
	return s
}

// MarshalJSON marshals the entry's schema to JSON. It takes
// a value receiver so that the entry schema's still marshalled
// when it's referenced as an interface{} object. See
// https://stackoverflow.com/a/21394657 for more details.
//
// Note that UnmarshalJSON is not implemented since that is not
// how plugin.EntrySchema objects are meant to be used.
func (s EntrySchema) MarshalJSON() ([]byte, error) {
	graph := s.graph
	if graph == nil {
		if _, ok := s.entry.(externalPlugin); ok {
			// We should never hit this code-path because external plugins with
			// unknown schemas will return a nil schema. Thus, EntrySchema#MarshalJSON
			// will never be invoked.
			msg := fmt.Sprintf(
				"s.MarshalJSON: called with a nil graph for external plugin entry %v (type ID %v)",
				CName(s.entry),
				s.TypeID,
			)
			panic(msg)
		}
		// We're marshalling a core plugin entry's schema. Note that the reason
		// we use an ordered map is to ensure that the first key in the marshalled
		// schema corresponds to s.
		graph = linkedhashmap.New()
		s.fill(graph)
	}
	return graph.ToJSON()
}

// IsSingleton marks the entry as a singleton entry.
func (s *EntrySchema) IsSingleton() *EntrySchema {
	s.entrySchema.Singleton = true
	return s
}

// SetMetaAttributeSchema sets the meta attribute's schema. obj is an empty struct
// that will be marshalled into a JSON schema. SetMetaSchema will panic
// if obj is not a struct.
func (s *EntrySchema) SetMetaAttributeSchema(obj interface{}) *EntrySchema {
	// We need to know if s.entry has any wrapped types in order to correctly
	// compute the schema. However that information is known when s.fill() is
	// called. Thus, we'll set the schema object to obj so s.fill() can properly
	// calculate the schema.
	s.metaAttributeSchemaObj = obj
	return s
}

// SetMetadataSchema sets Entry#Metadata's schema. obj is an empty struct that will be
// marshalled into a JSON schema. SetMetadataSchema will panic if obj is not a struct.
//
// NOTE: Only use SetMetadataSchema if you're overriding Entry#Metadata. Otherwise, use
// SetMetaAttributeSchema.
func (s *EntrySchema) SetMetadataSchema(obj interface{}) *EntrySchema {
	// See the comments in SetMetaAttributeSchema to understand why this line's necessary
	s.metadataSchemaObj = obj
	return s
}

func (s *EntrySchema) fill(graph *linkedhashmap.Map) {
	// Fill-in the meta attribute + metadata schemas
	var err error
	if s.metaAttributeSchemaObj != nil {
		s.entrySchema.MetaAttributeSchema, err = s.schemaOf(s.metaAttributeSchemaObj)
		if err != nil {
			s.fillPanicf("bad value passed into SetMetaAttributeSchema: %v", err)
		}
	}
	if s.metadataSchemaObj != nil {
		s.entrySchema.MetadataSchema, err = s.schemaOf(s.metadataSchemaObj)
		if err != nil {
			s.fillPanicf("bad value passed into SetMetadataSchema: %v", err)
		}
	}
	graph.Put(s.TypeID, &s.entrySchema)

	// Fill-in the children
	if !ListAction().IsSupportedOn(s.entry) {
		return
	}
	// "sParent" is read as "s.parent"
	sParent := s.entry.(Parent)
	children := sParent.ChildSchemas()
	if children == nil {
		s.fillPanicf("ChildSchemas() returned nil")
	}
	for _, child := range children {
		if child == nil {
			s.fillPanicf("found a nil child schema")
		}
		s.entrySchema.Children = append(s.Children, child.TypeID)
		if _, ok := graph.Get(child.TypeID); ok {
			continue
		}
		if child.graph != nil {
			if s.Label != registrySchemaLabel {
				// We should never hit this code-path
				s.fillPanicf("%v is an external plugin's schema", child)
			}
			child.graph.Each(func(key interface{}, value interface{}) {
				graph.Put(key, value)
			})
			continue
		}
		passAlongWrappedTypes(sParent, child.entry)
		child.fill(graph)
	}
}

// This helper's used by CachedList + EntrySchema#fill(). The reason for
// the helper is because /fs/schema uses repeated calls to CachedList when
// fetching the entry, so we need to pass-along the wrapped types when
// searching for it. However, Parent#ChildSchemas uses empty Entry objects
// that do not go through CachedList (by definition). Thus, the entry found
// by /fs/schema needs to pass its wrapped types along to the children to
// determine their metadata schemas. This is done in s.fill().
func passAlongWrappedTypes(p Parent, child Entry) {
	var wrappedTypes SchemaMap
	if root, ok := child.(HasWrappedTypes); ok {
		wrappedTypes = root.WrappedTypes()
	} else {
		wrappedTypes = p.wrappedTypes()
	}
	child.setWrappedTypes(wrappedTypes)
}

// Helper that wraps the common code shared by the SetMeta*Schema methods
func (s *EntrySchema) schemaOf(obj interface{}) (*JSONSchema, error) {
	typeMappings := make(map[reflect.Type]*jsonschema.Type)
	for t, s := range s.entry.wrappedTypes() {
		typeMappings[reflect.TypeOf(t)] = s.Type
	}
	r := jsonschema.Reflector{
		AllowAdditionalProperties: false,
		// Setting this option ensures that the schema's root is obj's
		// schema instead of a reference to a definition containing obj's
		// schema. This way, we can validate that "obj" is a JSON object's
		// schema. Otherwise, the check below will always fail.
		ExpandedStruct: true,
		TypeMappings:   typeMappings,
	}
	schema := r.Reflect(obj)
	if schema.Type.Type != "object" {
		return nil, fmt.Errorf("expected a JSON object but got %v", schema.Type.Type)
	}
	return schema, nil
}

// Helper for s.fill(). We make it a separate method to avoid re-creating
// closures for each recursive s.fill() call.
func (s *EntrySchema) fillPanicf(format string, a ...interface{}) {
	formatStr := fmt.Sprintf("s.fill (%v): %v", s.TypeID, format)
	msg := fmt.Sprintf(formatStr, a...)
	panic(msg)
}

func unravelPtr(t reflect.Type) reflect.Type {
	if t.Kind() == reflect.Ptr {
		return unravelPtr(t.Elem())
	}
	return t
}
