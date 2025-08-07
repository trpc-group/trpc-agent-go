package counter

func GetCounter(n int) int {
	counter := 0
	for i := 0; i < n; i++ {
		go func() {
			counter++
		}()
	}
	return counter
}
