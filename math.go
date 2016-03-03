package main

import "math"

func stdDev(numbers []float64, mean float64) float64 {
	total := 0.0
	for _, number := range numbers {
		total += math.Pow(number-mean, 2)
	}
	variance := total / float64(len(numbers)-1)
	return math.Sqrt(variance)
}

func sum(numbers []float64) (total float64) {
	for _, x := range numbers {
		total += x
	}
	return total
}

func getSetMax(numbers []float64) int {
	ret := int(-1)
	thisMax := float64(0.0)
	for i, v := range numbers {
		if v > thisMax {
			thisMax = v
			ret = i
		}
	}
	return ret
}

func removeOutliers(numbers []float64) (float64, float64) {
	mean := sum(numbers) / float64(len(numbers))
	stdev := stdDev(numbers, mean)

	for len(numbers) > 0 && stdev > 50.0 {
		i := getSetMax(numbers)

		// Delete the element from the array.
		if i+1 >= len(numbers) {
			numbers = numbers[:i]
		} else {
			numbers = append(numbers[:i], numbers[i+1:]...)
		}

		mean = sum(numbers) / float64(len(numbers))
		stdev = stdDev(numbers, mean)
	}

	return mean, stdev
}
