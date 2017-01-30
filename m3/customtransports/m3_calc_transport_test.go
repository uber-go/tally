package customtransport

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTCalcTransport(t *testing.T) {
	trans := &TCalcTransport{}
	require.Nil(t, trans.Open())
	require.True(t, trans.IsOpen())
	require.EqualValues(t, 0, trans.GetCount())

	testString1 := "test"
	testString2 := "string"
	n, err := trans.Write([]byte(testString1))
	require.Equal(t, len(testString1), n)
	require.Nil(t, err)
	require.EqualValues(t, len(testString1), trans.GetCount())
	n, err = trans.Write([]byte(testString2))
	require.EqualValues(t, len(testString2), n)
	require.Nil(t, err)
	require.EqualValues(t, len(testString1)+len(testString2), trans.GetCount())

	n, err = trans.Read([]byte(testString1))
	require.Nil(t, err)
	require.EqualValues(t, 0, n)
	require.Equal(t, ^uint64(0), trans.RemainingBytes())

	trans.ResetCount()
	require.EqualValues(t, 0, trans.GetCount())

	err = trans.Flush()
	require.Nil(t, err)
	require.Nil(t, trans.Close())
}
