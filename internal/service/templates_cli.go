package service

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/cautem/cautem-cli/internal/storage/templates"
)

// TemplateCreate saves a local sandbox template.
func (a *App) TemplateCreate(t templates.Template) error {
	if err := templates.Save(t); err != nil {
		return err
	}
	if c, err := a.gatewayClient(); err == nil {
		ctx, cancel := a.withTimeout(TimeoutAPIShort)
		defer cancel()
		if _, err := c.Healthz(ctx); err == nil {
			b, _ := json.Marshal(t)
			_ = b // gateway templates optional; local is source of truth for CLI
		}
	}
	fmt.Printf("template %s saved\n", t.Name)
	return nil
}

// TemplateList lists local templates.
func (a *App) TemplateList() error {
	list, err := templates.List()
	if err != nil {
		return err
	}
	if len(list) == 0 {
		fmt.Println("templates: (none)")
		return nil
	}
	for _, t := range list {
		fmt.Printf("%s\timage=%s from=%s\n", t.Name, t.Image, t.From)
	}
	return nil
}

// TemplateGet prints one template as JSON.
func (a *App) TemplateGet(name string) error {
	t, err := templates.Get(name)
	if err != nil {
		return err
	}
	b, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return err
	}
	os.Stdout.Write(b)
	if len(b) > 0 && b[len(b)-1] != '\n' {
		fmt.Println()
	}
	return nil
}

// TemplateDelete removes a local template.
func (a *App) TemplateDelete(name string) error {
	if err := templates.Delete(name); err != nil {
		return err
	}
	fmt.Printf("template %s deleted\n", name)
	return nil
}

// mergeTemplateIntoCreate applies template defaults before CLI flags (template first).
func mergeTemplateIntoCreate(opt *SandboxCreateOpts, tpl templates.Template) {
	if tpl.Image != "" && opt.Image == "" {
		opt.Image = tpl.Image
	}
	if tpl.From != "" && opt.From == "" {
		opt.From = tpl.From
	}
	if tpl.Policy != "" && opt.Policy == "" {
		opt.Policy = tpl.Policy
	}
	if tpl.CPU > 0 && opt.CPU == 0 {
		opt.CPU = tpl.CPU
	}
	if tpl.Memory != "" && opt.Memory == "" {
		opt.Memory = tpl.Memory
	}
	if tpl.PidsLimit != 0 && opt.PidsLimit == 0 {
		opt.PidsLimit = tpl.PidsLimit
	}
	if len(tpl.Providers) > 0 && len(opt.Providers) == 0 {
		opt.Providers = append([]string{}, tpl.Providers...)
	}
	if tpl.Env != nil {
		if opt.Env == nil {
			opt.Env = map[string]string{}
		}
		for k, v := range tpl.Env {
			if _, ok := opt.Env[k]; !ok {
				opt.Env[k] = v
			}
		}
	}
	if tpl.Labels != nil {
		if opt.Labels == nil {
			opt.Labels = map[string]string{}
		}
		for k, v := range tpl.Labels {
			if _, ok := opt.Labels[k]; !ok {
				opt.Labels[k] = v
			}
		}
	}
	opt.Forwards = append(opt.Forwards, tpl.Forwards...)
}
