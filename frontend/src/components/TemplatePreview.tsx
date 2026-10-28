import { ExternalLink, Image as ImageIcon, Reply } from "lucide-react";
import { cx } from "./ui";
import type { TemplateSpec, Variables } from "../lib/types";

/** Fill {{1}} / {{name}} placeholders with values (or examples when empty). */
function fill(text: string, params: { name?: string; example: string }[] | undefined, values: string[] | undefined, sampleName: string) {
  if (!text) return "";
  const lookup = new Map<string, string>();
  (params ?? []).forEach((p, idx) => {
    const v = values?.[idx]?.trim() || p.example || `{{${p.name ?? idx + 1}}}`;
    const personalised = v.replaceAll("{first_name}", sampleName.split(" ")[0]).replaceAll("{name}", sampleName);
    lookup.set(p.name ?? String(idx + 1), personalised);
  });
  return text.replace(/\{\{\s*(\w+)\s*\}\}/g, (m, key: string) => {
    return lookup.get(key) ?? m;
  });
}

/** Renders WhatsApp's *bold*, _italic_ and ~strike~ markers. */
function Rich({ text }: { text: string }) {
  const parts = text.split(/(\*[^*\n]+\*|_[^_\n]+_|~[^~\n]+~)/g);
  return (
    <>
      {parts.map((p, i) => {
        if (/^\*[^*]+\*$/.test(p)) return <strong key={i}>{p.slice(1, -1)}</strong>;
        if (/^_[^_]+_$/.test(p)) return <em key={i}>{p.slice(1, -1)}</em>;
        if (/^~[^~]+~$/.test(p)) return <s key={i}>{p.slice(1, -1)}</s>;
        return <span key={i}>{p}</span>;
      })}
    </>
  );
}

export default function TemplatePreview({ spec, values, sampleName = "Sara", size = "md" }: { spec: TemplateSpec; values?: Partial<Variables>; sampleName?: string; size?: "md" | "lg" }) {
  const lg = size === "lg";
  const media = values?.header_media_url || spec.header_example_url;
  return (
    <div className={cx("rounded-xl bg-[#e5ddd5]", lg ? "px-6 py-8 sm:px-8" : "p-4")} style={{ backgroundImage: "radial-gradient(rgba(0,0,0,0.04) 1px, transparent 1px)", backgroundSize: "12px 12px" }}>
      <div className={cx("rounded-lg rounded-tl-none bg-white leading-snug text-slate-800 shadow-sm", lg ? "max-w-sm text-[15px]" : "max-w-xs text-[14px]")}>
        {spec.header_format === "image" && (
          <div className="p-1">
            {media ? (
              <img src={media} alt="" className="max-h-56 w-full rounded-md object-cover" onError={(e) => ((e.target as HTMLImageElement).style.display = "none")} />
            ) : (
              <div className="flex h-36 items-center justify-center rounded-md bg-slate-100 text-slate-400">
                <ImageIcon className="size-8" />
              </div>
            )}
          </div>
        )}
        {["video", "document", "gif"].includes(spec.header_format) && (
          <div className="m-1 rounded-md bg-slate-100 p-6 text-center text-xs text-slate-500">{spec.header_format} header</div>
        )}
        <div className="px-3 pb-1.5 pt-2">
          {spec.header_format === "text" && spec.header_text && (
            <p className="mb-1 font-semibold">{fill(spec.header_text, spec.header_params, values?.header_text, sampleName)}</p>
          )}
          <p className="whitespace-pre-wrap break-words">
            <Rich text={fill(spec.body, spec.body_params, values?.body, sampleName)} />
          </p>
          {spec.footer && <p className="mt-1 text-xs text-slate-500">{spec.footer}</p>}
          <p className="mt-0.5 text-right text-[10px] text-slate-400">10:30</p>
        </div>
        {(spec.buttons ?? []).map((b, i) => (
          <div key={i} className="flex items-center justify-center gap-1.5 border-t border-slate-100 py-2 text-sm font-medium text-sky-600">
            {b.type === "url" ? <ExternalLink className="size-4" /> : <Reply className="size-4" />}
            {b.text || b.type}
          </div>
        ))}
      </div>
    </div>
  );
}
