package cli

import (
	"github.com/spf13/cobra"

	"github.com/mittwald/api-kit/definition"
	"github.com/mittwald/api-kit/engine"
)

// getCommand returns the command that shows the items of a resource.
func (a *App) getCommand(doc *definition.Document) *cobra.Command {
	var (
		filters  []string
		limit    int
		page     int
		skip     int
		sortBy   string
		sortDesc bool
		all      bool
	)
	cmd := &cobra.Command{
		Use:   "get RESOURCE [ID] [SUB-RESOURCE]",
		Short: "Show the items of a resource, one item, or what belongs to it",
		Long: "Show the items of a resource. With an ID, show that one item; with a\n" +
			"sub-resource as well, show the items that belong to it.\n\n" +
			"An ID can be anything that identifies the item, such as a short ID or a\n" +
			"name, where the definition of the resource says how to look it up.",
		Args:              cobra.RangeArgs(1, 3),
		ValidArgsFunction: completeGetArgs(doc),
		RunE: func(cmd *cobra.Command, args []string) error {
			// A mistake in the output format is reported before anything
			// is requested.
			spec, err := a.globals.spec()
			if err != nil {
				return err
			}
			request := engine.GetRequest{Resource: doc.ResourceName(args[0]), All: all}
			if len(args) > 1 {
				request.ID = args[1]
			}
			if len(args) > 2 {
				request.Sub = doc.ResourceName(args[2])
			}
			if request.Filters, err = engine.ParseFilters(filters); err != nil {
				return err
			}
			flags := cmd.Flags()
			if flags.Changed("limit") {
				request.Limit = &limit
			}
			if flags.Changed("page") {
				request.Page = &page
			}
			if flags.Changed("skip") {
				request.Skip = &skip
			}

			session, err := a.session(doc)
			if err != nil {
				return err
			}
			result, err := session.engine.Get(cmd.Context(), request)
			if err != nil {
				return failed(err)
			}
			return a.render(cmd, session, result, view{spec: spec, sortBy: sortBy, sortDesc: sortDesc})
		},
	}
	flags := cmd.Flags()
	flags.StringArrayVarP(&filters, "filter", "f", nil, "filter the items, as key=value; several filters are separated by commas or given one by one")
	flags.IntVarP(&limit, "limit", "L", 0, "number of items to fetch, or per page with --all")
	flags.IntVarP(&page, "page", "P", 0, "page to fetch")
	flags.IntVarP(&skip, "skip", "S", 0, "number of items to skip")
	flags.StringVarP(&sortBy, "sort-by", "s", "", "field to sort the items by")
	flags.BoolVarP(&sortDesc, "sort-desc", "d", false, "sort in descending order")
	flags.BoolVarP(&all, "all", "A", false, "fetch every page")
	cmd.MarkFlagsMutuallyExclusive("page", "skip")
	if err := cmd.RegisterFlagCompletionFunc("filter", completeFilter(doc)); err != nil {
		panic(err) // the flag was registered above
	}
	return cmd
}
