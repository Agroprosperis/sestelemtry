package dispatch

import (
	"errors"
	"math"
)

// errLPNoConvergence mirrors the mockup's iteration guard.
var errLPNoConvergence = errors.New("dispatch: LP did not converge")

// solveLP maximizes c·x subject to A·x ≤ b and x ≥ 0: a two-phase
// simplex with Bland-style tie breaking. It is a line-for-line port of
// solveLP in the dispatch desk mockup (ems-spec
// reference/client_reports/ems_control_mockup/ems-dispatch-desk-browser-v2.html)
// so a plan computed here is the plan the client approved in the demo.
// Returns (nil, nil) when the program is infeasible or unbounded.
func solveLP(A [][]float64, b, c []float64) ([]float64, error) {
	const eps = 1e-8
	m, n := len(b), len(c)
	B := make([]int, m)
	N := make([]int, n+1)
	D := make([][]float64, m+2)
	for i := range D {
		D[i] = make([]float64, n+2)
	}
	for i := 0; i < m; i++ {
		copy(D[i][:n], A[i])
		B[i] = n + i
		D[i][n] = -1
		D[i][n+1] = b[i]
	}
	for j := 0; j < n; j++ {
		N[j] = j
		D[m][j] = -c[j]
	}
	N[n] = -1
	D[m+1][n] = 1

	pivot := func(r, s int) {
		inv := 1 / D[r][s]
		rowR := D[r]
		for i := 0; i < m+2; i++ {
			if i == r {
				continue
			}
			f := D[i][s] * inv
			if f == 0 {
				continue
			}
			row := D[i]
			for j := 0; j < n+2; j++ {
				if j != s {
					row[j] -= rowR[j] * f
				}
			}
		}
		for j := 0; j < n+2; j++ {
			if j != s {
				rowR[j] *= inv
			}
		}
		for i := 0; i < m+2; i++ {
			if i != r {
				D[i][s] *= -inv
			}
		}
		rowR[s] = inv
		B[r], N[s] = N[s], B[r]
	}

	simplex := func(phase int) (bool, error) {
		row := m
		if phase == 1 {
			row = m + 1
		}
		for iteration := 0; iteration < 10000; iteration++ {
			s := -1
			for j := 0; j <= n; j++ {
				if phase == 2 && N[j] == -1 {
					continue
				}
				if s < 0 || D[row][j] < D[row][s]-eps || (math.Abs(D[row][j]-D[row][s]) <= eps && N[j] < N[s]) {
					s = j
				}
			}
			if s < 0 || D[row][s] >= -eps {
				return true, nil
			}
			r := -1
			for i := 0; i < m; i++ {
				if D[i][s] <= eps {
					continue
				}
				ratio := D[i][n+1] / D[i][s]
				best := math.Inf(1)
				if r >= 0 {
					best = D[r][n+1] / D[r][s]
				}
				if r < 0 || ratio < best-eps || (math.Abs(ratio-best) <= eps && B[i] < B[r]) {
					r = i
				}
			}
			if r < 0 {
				return false, nil
			}
			pivot(r, s)
		}
		return false, errLPNoConvergence
	}

	r := 0
	for i := 1; i < m; i++ {
		if D[i][n+1] < D[r][n+1] {
			r = i
		}
	}
	if m > 0 && D[r][n+1] < -eps {
		pivot(r, n)
		ok, err := simplex(1)
		if err != nil {
			return nil, err
		}
		if !ok || D[m+1][n+1] < -eps || math.Abs(D[m+1][n+1]) > eps {
			return nil, nil
		}
		for i := 0; i < m; i++ {
			if B[i] != -1 {
				continue
			}
			s := -1
			for j := 0; j <= n; j++ {
				if math.Abs(D[i][j]) > eps && (s < 0 || N[j] < N[s]) {
					s = j
				}
			}
			if s >= 0 {
				pivot(i, s)
			}
		}
	}
	ok, err := simplex(2)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	x := make([]float64, n)
	for i := 0; i < m; i++ {
		if B[i] < n && B[i] >= 0 {
			x[B[i]] = D[i][n+1]
		}
	}
	return x, nil
}
