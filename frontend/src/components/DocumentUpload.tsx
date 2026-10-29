import { useRef, useState } from "react";
import { ExternalLink, FileText, Trash2, Upload } from "lucide-react";
import { api } from "../lib/api";
import { Button, useToast } from "./ui";

const MAX_BYTES = 25 * 1024 * 1024;

/** Uploads a PDF to the server and reports its public URL and display name. */
export default function DocumentUpload({
  label,
  url,
  filename,
  onChange,
}: {
  label: string;
  url: string;
  filename: string;
  onChange: (url: string, filename: string) => void;
}) {
  const toast = useToast();
  const input = useRef<HTMLInputElement>(null);
  const [busy, setBusy] = useState(false);

  const upload = async (file: File) => {
    if (file.type !== "application/pdf" && !file.name.toLowerCase().endsWith(".pdf")) {
      toast.error("Choose a PDF file.");
      return;
    }
    if (file.size > MAX_BYTES) {
      toast.error("The PDF must be 25 MB or smaller.");
      return;
    }
    setBusy(true);
    try {
      const fd = new FormData();
      fd.append("file", file);
      const r = await api.post<{ url: string; filename: string }>("/uploads/document", fd);
      onChange(r.url, r.filename);
    } catch (err) {
      toast.error(err);
    } finally {
      setBusy(false);
      if (input.current) input.current.value = "";
    }
  };

  return (
    <div>
      <span className="mb-1 block text-sm font-medium text-slate-700">{label}</span>
      <div
        className="flex items-center gap-4 rounded-lg border border-dashed border-slate-300 bg-slate-50 p-3"
        onDragOver={(e) => e.preventDefault()}
        onDrop={(e) => {
          e.preventDefault();
          const f = e.dataTransfer.files?.[0];
          if (f) upload(f);
        }}
      >
        <div className="flex size-16 shrink-0 items-center justify-center rounded-md bg-white ring-1 ring-slate-200">
          <FileText className={url ? "size-8 text-red-600" : "size-8 text-slate-300"} />
        </div>
        <div className="min-w-0 space-y-1.5">
          {url && (
            <a href={url} target="_blank" rel="noreferrer" className="flex items-center gap-1 truncate text-sm font-medium text-brand-700 hover:underline">
              {filename || "document.pdf"} <ExternalLink className="size-3.5 shrink-0" />
            </a>
          )}
          <div className="flex flex-wrap gap-2">
            <Button type="button" variant="secondary" size="sm" loading={busy} onClick={() => input.current?.click()}>
              <Upload className="size-3.5" /> {url ? "Change PDF" : "Upload PDF"}
            </Button>
            {url && (
              <Button type="button" variant="ghost" size="sm" onClick={() => onChange("", "")}>
                <Trash2 className="size-3.5" /> Remove
              </Button>
            )}
          </div>
          <p className="text-xs text-slate-500">PDF up to 25 MB. Customers see the file name shown above.</p>
        </div>
      </div>
      <input
        ref={input}
        type="file"
        accept="application/pdf,.pdf"
        className="hidden"
        onChange={(e) => {
          const f = e.target.files?.[0];
          if (f) upload(f);
        }}
      />
    </div>
  );
}
