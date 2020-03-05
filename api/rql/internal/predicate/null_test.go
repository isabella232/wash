package predicate

import (
	"testing"

	"github.com/puppetlabs/wash/api/rql"
	"github.com/puppetlabs/wash/api/rql/ast/asttest"
	"github.com/puppetlabs/wash/api/rql/internal/predicate/expression"
	"github.com/stretchr/testify/suite"
)

type NullTestSuite struct {
	PrimitiveValueTestSuite
}

func (s *NullTestSuite) TestMarshal() {
	s.MTC(Null(), nil)
}

func (s *NullTestSuite) TestUnmarshal() {
	n := Null()
	s.UMETC(n, "foo", ".*null", true)
	s.UMTC(n, nil, Null())
}

func (s *NullTestSuite) TestEvalValue() {
	n := Null()
	s.EVFTC(n, "foo", 1, true)
	s.EVTTC(n, nil)
}

func (s NullTestSuite) TestEvalValueSchema() {
	n := Null()
	s.EVSFTC(n, s.VS("object", "array")...)
	s.EVSTTC(n, s.VS("null")...)
}

func (s *NullTestSuite) TestExpression_AtomAndNot() {
	expr := expression.New("null", true, func() rql.ASTNode {
		return Null()
	})

	s.MUM(expr, nil)
	s.EVFTC(expr, "foo", 1, true)
	s.EVTTC(expr, nil)
	s.EVSFTC(expr, s.VS("object", "array")...)
	s.EVSTTC(expr, s.VS("null")...)
	s.AssertNotImplemented(
		expr,
		asttest.EntryPredicateC,
		asttest.EntrySchemaPredicateC,
		asttest.StringPredicateC,
		asttest.NumericPredicateC,
		asttest.TimePredicateC,
		asttest.ActionPredicateC,
	)

	s.MUM(expr, []interface{}{"NOT", nil})
	s.EVTTC(expr, "foo", 1, true)
	s.EVFTC(expr, nil)
	s.EVSTTC(expr, s.VS("null", "object", "array")...)
}

func TestNull(t *testing.T) {
	suite.Run(t, new(NullTestSuite))
}
