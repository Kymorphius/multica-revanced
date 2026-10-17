package main

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/multica-ai/multica/server/internal/cli"
)

var memoryCmd = newMemoryCommand()

func newMemoryCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "memory", Short: "Save and recall shared workspace and project memories"}
	list := &cobra.Command{Use: "list", Short: "List or search shared memories", Args: cobra.NoArgs, RunE: runMemoryList}
	list.Flags().String("query", "", "Search literal text in titles and content")
	list.Flags().String("project-id", "", "Filter to a project's memories")
	list.Flags().Int("limit", 100, "Page size (1 to 100)")
	list.Flags().Int("offset", 0, "Number of memories to skip")
	get := &cobra.Command{Use: "get <id>", Short: "Read a memory and its current revision", Args: exactArgs(1), RunE: runMemoryGet}
	create := &cobra.Command{Use: "create", Short: "Save a memory shared with this workspace or project", Args: cobra.NoArgs, RunE: runMemoryCreate}
	create.Flags().String("title", "", "Short memory title (required)")
	create.Flags().String("project-id", "", "Project scope; omit to share across the workspace")
	create.Flags().String("source", "manual", "Memory source: manual or automatic (issue tasks only)")
	update := &cobra.Command{Use: "update <id>", Short: "Correct a memory using its current revision", Args: exactArgs(1), RunE: runMemoryUpdate}
	update.Flags().String("title", "", "New memory title")
	update.Flags().Int("expected-revision", 0, "Current revision from memory get (required)")
	for _, c := range []*cobra.Command{create, update} {
		c.Flags().String("content", "", "Memory text")
		c.Flags().Bool("content-stdin", false, "Read memory text from stdin")
		c.Flags().String("content-file", "", "Read memory text from a UTF-8 file in the working directory")
		c.Flags().Bool("allow-external-file", false, "Allow reading a content file outside the working directory")
	}
	del := &cobra.Command{Use: "delete <id>", Short: "Forget a shared memory", Args: exactArgs(1), RunE: runMemoryDelete}
	del.Flags().Int("expected-revision", 0, "Current revision from memory get (required)")
	del.Flags().Bool("yes", false, "Confirm deletion of the shared memory")
	settings := &cobra.Command{Use: "settings", Short: "Manage automatic extraction of issue-task experience"}
	settingsGet := &cobra.Command{Use: "get", Short: "Read memory settings", Args: cobra.NoArgs, RunE: runMemorySettingsGet}
	settingsUpdate := &cobra.Command{Use: "update", Short: "Enable or disable automatic extraction (workspace admins)", Args: cobra.NoArgs, RunE: runMemorySettingsUpdate}
	settingsUpdate.Flags().Bool("automatic-extraction", false, "Automatically extract reusable issue-task experience; use =true or =false")
	for _, c := range []*cobra.Command{list, get, create, update, del, settingsGet, settingsUpdate} {
		c.Flags().String("output", "json", "Output format: json or table")
	}
	settings.AddCommand(settingsGet, settingsUpdate)
	cmd.AddCommand(list, get, create, update, del, settings)
	return cmd
}

func memoryAPI(cmd *cobra.Command, call func(context.Context, *cli.APIClient) error) error {
	format, _ := cmd.Flags().GetString("output")
	if format != "json" && format != "table" {
		return fmt.Errorf("--output must be json or table")
	}
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()
	return call(ctx, client)
}

func printMemory(cmd *cobra.Command, value map[string]any) error {
	format, _ := cmd.Flags().GetString("output")
	if format == "json" {
		return cli.PrintJSON(cmd.OutOrStdout(), value)
	}
	for _, key := range []string{"id", "title", "project_id", "source", "revision", "content", "automatic_extraction", "deleted"} {
		if v, ok := value[key]; ok && v != nil {
			fmt.Fprintf(cmd.OutOrStdout(), "%s: %v\n", key, v)
		}
	}
	return nil
}

func runMemoryList(cmd *cobra.Command, _ []string) error {
	limit, _ := cmd.Flags().GetInt("limit")
	offset, _ := cmd.Flags().GetInt("offset")
	if limit < 1 || limit > 100 || offset < 0 || int64(offset) > 2147483647 {
		return fmt.Errorf("--limit must be 1 to 100 and --offset must be 0 to 2147483647")
	}
	q := url.Values{"limit": {strconv.Itoa(limit)}, "offset": {strconv.Itoa(offset)}}
	query, _ := cmd.Flags().GetString("query")
	project, _ := cmd.Flags().GetString("project-id")
	if query != "" {
		q.Set("q", query)
	}
	if project != "" {
		q.Set("project_id", project)
	}
	return memoryAPI(cmd, func(ctx context.Context, client *cli.APIClient) error {
		var rows []map[string]any
		if err := client.GetJSON(ctx, "/api/memories?"+q.Encode(), &rows); err != nil {
			return fmt.Errorf("list memories: %w", err)
		}
		if rows == nil {
			rows = []map[string]any{}
		}
		format, _ := cmd.Flags().GetString("output")
		if format == "json" {
			return cli.PrintJSON(cmd.OutOrStdout(), rows)
		}
		w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "ID\tREVISION\tSOURCE\tPROJECT\tTITLE")
		for _, m := range rows {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", strVal(m, "id"), strVal(m, "revision"), strVal(m, "source"), strVal(m, "project_id"), strings.NewReplacer("\n", " ", "\t", " ").Replace(strVal(m, "title")))
		}
		return w.Flush()
	})
}

func runMemoryGet(cmd *cobra.Command, args []string) error {
	return memoryAPI(cmd, func(ctx context.Context, client *cli.APIClient) error {
		var result map[string]any
		if err := client.GetJSON(ctx, "/api/memories/"+url.PathEscape(args[0]), &result); err != nil {
			return fmt.Errorf("get memory: %w", err)
		}
		return printMemory(cmd, result)
	})
}

func runMemoryCreate(cmd *cobra.Command, _ []string) error {
	title, _ := cmd.Flags().GetString("title")
	content, supplied, err := resolveTextFlag(cmd, "content")
	if err != nil {
		return err
	}
	if strings.TrimSpace(title) == "" || !supplied || strings.TrimSpace(content) == "" {
		return fmt.Errorf("--title and memory content are required")
	}
	source, _ := cmd.Flags().GetString("source")
	if source != "manual" && source != "automatic" {
		return fmt.Errorf("--source must be manual or automatic")
	}
	body := map[string]any{"title": title, "content": content, "source": source}
	if project, _ := cmd.Flags().GetString("project-id"); project != "" {
		body["project_id"] = project
	}
	return memoryAPI(cmd, func(ctx context.Context, client *cli.APIClient) error {
		var result map[string]any
		if err := client.PostJSON(ctx, "/api/memories", body, &result); err != nil {
			return fmt.Errorf("save memory: %w", err)
		}
		return printMemory(cmd, result)
	})
}

func memoryRevision(cmd *cobra.Command) (int, error) {
	r, _ := cmd.Flags().GetInt("expected-revision")
	if r < 1 || int64(r) > 2147483647 {
		return 0, fmt.Errorf("--expected-revision must be a positive revision from memory get")
	}
	return r, nil
}

func runMemoryUpdate(cmd *cobra.Command, args []string) error {
	revision, err := memoryRevision(cmd)
	if err != nil {
		return err
	}
	body := map[string]any{"expected_revision": revision}
	if cmd.Flags().Changed("title") {
		title, _ := cmd.Flags().GetString("title")
		body["title"] = title
	}
	content, supplied, err := resolveTextFlag(cmd, "content")
	if err != nil {
		return err
	}
	if supplied {
		body["content"] = content
	}
	if len(body) == 1 {
		return fmt.Errorf("provide --title or memory content to update")
	}
	return memoryAPI(cmd, func(ctx context.Context, client *cli.APIClient) error {
		var result map[string]any
		if err := client.PutJSON(ctx, "/api/memories/"+url.PathEscape(args[0]), body, &result); err != nil {
			return fmt.Errorf("update memory: %w", err)
		}
		return printMemory(cmd, result)
	})
}

func runMemoryDelete(cmd *cobra.Command, args []string) error {
	revision, err := memoryRevision(cmd)
	if err != nil {
		return err
	}
	if yes, _ := cmd.Flags().GetBool("yes"); !yes {
		return fmt.Errorf("use --yes to confirm forgetting this shared memory")
	}
	return memoryAPI(cmd, func(ctx context.Context, client *cli.APIClient) error {
		path := "/api/memories/" + url.PathEscape(args[0]) + "?expected_revision=" + strconv.Itoa(revision)
		if err := client.DeleteJSON(ctx, path); err != nil {
			return fmt.Errorf("delete memory: %w", err)
		}
		return printMemory(cmd, map[string]any{"id": args[0], "deleted": true})
	})
}

func runMemorySettingsGet(cmd *cobra.Command, _ []string) error {
	return memoryAPI(cmd, func(ctx context.Context, client *cli.APIClient) error {
		var result map[string]any
		if err := client.GetJSON(ctx, "/api/memories/settings", &result); err != nil {
			return fmt.Errorf("get memory settings: %w", err)
		}
		return printMemory(cmd, result)
	})
}

func runMemorySettingsUpdate(cmd *cobra.Command, _ []string) error {
	if !cmd.Flags().Changed("automatic-extraction") {
		return fmt.Errorf("--automatic-extraction=true or --automatic-extraction=false is required")
	}
	enabled, _ := cmd.Flags().GetBool("automatic-extraction")
	return memoryAPI(cmd, func(ctx context.Context, client *cli.APIClient) error {
		var result map[string]any
		if err := client.PutJSON(ctx, "/api/memories/settings", map[string]any{"automatic_extraction": enabled}, &result); err != nil {
			return fmt.Errorf("update memory settings: %w", err)
		}
		return printMemory(cmd, result)
	})
}
