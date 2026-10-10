package ppocr

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDefaultDictLayout(t *testing.T) {
	d := DefaultDict()
	require.Equal(t, 18708, d.Len())
	require.Equal(t, 18710, d.ClassCount())
	require.Equal(t, 18709, d.SpaceClass())

	cases := []struct {
		class int
		want  rune
		ok    bool
	}{
		{0, 0, false},
		{1, '!', true},
		{33, '0', true},
		{18709, ' ', true},
		{18710, 0, false},
	}
	for _, c := range cases {
		got, ok := d.At(c.class)
		require.Equal(t, c.ok, ok, "class %d", c.class)
		require.Equal(t, c.want, got, "class %d", c.class)
	}
	require.NoError(t, d.Validate(18710))
	require.Error(t, d.Validate(18709))
}

func TestParseDictSkipsBlankAndCR(t *testing.T) {
	d, err := ParseDict([]byte("a\r\n\nbc\n"))
	require.NoError(t, err)
	require.Equal(t, 2, d.Len())
	ch, _ := d.At(2)
	require.Equal(t, 'b', ch, "only the first rune of a line is used")
	_, err = ParseDict([]byte("\n\n"))
	require.Error(t, err)
}
