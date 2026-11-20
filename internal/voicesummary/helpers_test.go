package voicesummary_test

// errorText は err の文面を返す。nil は "<nil>" にして、問題がないことも文字列で比べられるようにする。
func errorText(err error) string {
	if err == nil {
		return "<nil>"
	}
	return err.Error()
}
