// Code generated by lister-gen. DO NOT EDIT.

package v1

import (
	v1 "github.com/openshift/api/config/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/tools/cache"
)

// DNSLister helps list DNSes.
// All objects returned here must be treated as read-only.
type DNSLister interface {
	// List lists all DNSes in the indexer.
	// Objects returned here must be treated as read-only.
	List(selector labels.Selector) (ret []*v1.DNS, err error)
	// Get retrieves the DNS from the index for a given name.
	// Objects returned here must be treated as read-only.
	Get(name string) (*v1.DNS, error)
	DNSListerExpansion
}

// dNSLister implements the DNSLister interface.
type dNSLister struct {
	indexer cache.Indexer
}

// NewDNSLister returns a new DNSLister.
func NewDNSLister(indexer cache.Indexer) DNSLister {
	return &dNSLister{indexer: indexer}
}

// List lists all DNSes in the indexer.
func (s *dNSLister) List(selector labels.Selector) (ret []*v1.DNS, err error) {
	err = cache.ListAll(s.indexer, selector, func(m interface{}) {
		ret = append(ret, m.(*v1.DNS))
	})
	return ret, err
}

// Get retrieves the DNS from the index for a given name.
func (s *dNSLister) Get(name string) (*v1.DNS, error) {
	obj, exists, err := s.indexer.GetByKey(name)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, errors.NewNotFound(v1.Resource("dns"), name)
	}
	return obj.(*v1.DNS), nil
}
