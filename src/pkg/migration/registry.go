package migration

import (
	"fmt"
	"sync"
)

// SchemaRegistry 模式注册表，管理所有数据库类型的模式定义
type SchemaRegistry struct {
	mu      sync.RWMutex
	schemas map[DatabaseType]*DatabaseSchema
}

// 全局模式注册表
var globalRegistry = &SchemaRegistry{
	schemas: make(map[DatabaseType]*DatabaseSchema),
}

// RegisterSchema 注册数据库模式
func RegisterSchema(schema *DatabaseSchema) error {
	return globalRegistry.Register(schema)
}

// GetSchema 获取数据库模式
func GetSchema(dbType DatabaseType) (*DatabaseSchema, error) {
	return globalRegistry.Get(dbType)
}

// ListSchemas 列出所有注册的模式
func ListSchemas() []DatabaseType {
	return globalRegistry.List()
}

// Register 注册数据库模式
func (r *SchemaRegistry) Register(schema *DatabaseSchema) error {
	if schema == nil {
		return fmt.Errorf("schema cannot be nil")
	}
	if schema.Type == "" {
		return fmt.Errorf("schema type cannot be empty")
	}
	if schema.MigrationSource == nil {
		return fmt.Errorf("schema migration source cannot be nil")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.schemas[schema.Type]; exists {
		return fmt.Errorf("schema type %s already registered", schema.Type)
	}

	r.schemas[schema.Type] = schema
	return nil
}

// Get 获取数据库模式
func (r *SchemaRegistry) Get(dbType DatabaseType) (*DatabaseSchema, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	schema, exists := r.schemas[dbType]
	if !exists {
		return nil, fmt.Errorf("schema type %s not registered", dbType)
	}
	return schema, nil
}

// List 列出所有注册的模式类型
func (r *SchemaRegistry) List() []DatabaseType {
	r.mu.RLock()
	defer r.mu.RUnlock()

	types := make([]DatabaseType, 0, len(r.schemas))
	for t := range r.schemas {
		types = append(types, t)
	}
	return types
}

// MustRegisterSchema 注册数据库模式，失败时panic
func MustRegisterSchema(schema *DatabaseSchema) {
	if err := RegisterSchema(schema); err != nil {
		panic(fmt.Sprintf("failed to register schema: %v", err))
	}
}
