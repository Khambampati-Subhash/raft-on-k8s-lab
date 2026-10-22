package watch

import (
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/Khambampati-Subhash/raft-on-k8s-lab/internal/client"
)

// Render writes one table row per node.
func Render(w io.Writer, cur []client.StatusResult) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NODE\tROLE\tTERM\tLEADER\tCOMMIT\tAPPLIED\tLAST-CONTACT\tVOTERS")
	for _, r := range cur {
		if r.Status == nil {
			fmt.Fprintf(tw, "%s\tunreachable\t-\t-\t-\t-\t-\t-\n", r.URL)
			continue
		}
		s := r.Status
		fmt.Fprintf(tw, "%s\t%s\t%d\t%s\t%d\t%d\t%s\t%d\n",
			s.ID, s.Role, s.Term, orDash(s.LeaderID), s.CommitIndex, s.AppliedIndex, orDash(s.LastContact), len(s.Servers))
	}
	tw.Flush()
}
