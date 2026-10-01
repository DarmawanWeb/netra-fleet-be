package api

import "testing"

func TestParseRoute(t *testing.T) {
	pts := parseRoute("# x,y,yaw_deg,dwell_sec\n0,0,0,0\n1.5,2,90,10\nbad,row\n3,4\n")
	if len(pts) != 3 || pts[1] != [4]float64{1.5, 2, 90, 10} || pts[2] != [4]float64{3, 4, 0, 0} {
		t.Fatalf("unexpected %v", pts)
	}
}
