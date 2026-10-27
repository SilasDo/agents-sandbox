package viperconfig

import (
	"strconv"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

// bindConfigFlags binds each config-backed flag found on cmd (local or
// inherited) to viper and mirrors its declared default so that an
// unspecified flag with a default does not override env/config. A declared flag
// is never nil, so BindPFlag cannot fail here.
func bindConfigFlags(v *viper.Viper, cmd *cobra.Command) {
	for _, key := range configFlagKeys {
		flag := findFlag(cmd, key)
		if flag == nil {
			continue
		}
		_ = v.BindPFlag(key, flag)
		v.SetDefault(key, flagTypedDefault(key, flag))
	}
}

func findFlag(cmd *cobra.Command, name string) *pflag.Flag {
	if f := cmd.Flags().Lookup(name); f != nil {
		return f
	}
	return cmd.InheritedFlags().Lookup(name)
}

func flagTypedDefault(key string, flag *pflag.Flag) any {
	switch key {
	case keyCPUs:
		n, _ := strconv.ParseUint(flag.DefValue, 10, 8)
		return uint8(n)
	case keyYes:
		return flag.DefValue == strTrue
	case keyQuiet, KeyOpenLinks:
		return flag.DefValue == strTrue
	default:
		return flag.DefValue
	}
}
