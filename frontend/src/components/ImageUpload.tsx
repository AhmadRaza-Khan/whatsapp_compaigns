import { useRef, useState } from "react";
import { Image as ImageIcon, Trash2, Upload } from "lucide-react";
import { api } from "../lib/api";
import { Button, useToast } from "./ui";

const MAX_BYTES = 5 * 1024 * 1024; // WhatsApp image header limit

/** Uploads a JPG/PNG to the server and reports its public URL. */
export default function ImageUpload({ label, value, onChange, optional }: { label: string; value: string; onChange: (url: string) => void; optional?: boolean }) {
  const toast = useToast();
  const input = useRef<HTMLInputElement>(null);
  const [busy, setBusy] = useState(false);

  const upload = async (file: File) => {
    if (!["image/jpeg", "image/png"].includes(file.type)) {
      toast.error("Use a JPG or PNG image. WhatsApp doesn't accept other formats.");
      return;
    }
    if (file.size > MAX_BYTES) {
      toast.error("The image must be 5 MB or smaller.");
      return;
    }
    setBusy(true);
    try {
      const fd = new FormData();
      fd.append("file", file);
      const r = await api.post<{ url: string }>("/uploads/image", fd);
      onChange(r.url);
    } catch (err) {
      toast.error(err);
    } finally {
      setBusy(false);
      if (input.current) input.current.value = "";
    }
  };

  return (
    <div>
      <span className="mb-1 block text-sm font-medium text-slate-700">
        {label}
        {optional && <span className="font-normal text-slate-400"> (optional)</span>}
      </span>
      <div
        className="flex items-center gap-4 rounded-lg border border-dashed border-slate-300 bg-slate-50 p-3"
        onDragOver={(e) => e.preventDefault()}
        onDrop={(e) => {
          e.preventDefault();
          const f = e.dataTransfer.files?.[0];
          if (f) upload(f);
        }}
      >
        {value ? (
          <img src={value} alt="" className="size-20 shrink-0 rounded-md object-cover ring-1 ring-slate-200" />
        ) : (
          <div className="flex size-20 shrink-0 items-center justify-center rounded-md bg-white text-slate-300 ring-1 ring-slate-200">
            <ImageIcon className="size-8" />
          </div>
        )}
        <div className="space-y-1.5">
          <div className="flex flex-wrap gap-2">
            <Button type="button" variant="secondary" size="sm" loading={busy} onClick={() => input.current?.click()}>
              <Upload className="size-3.5" /> {value ? "Change image" : "Upload image"}
            </Button>
            {value && (
              <Button type="button" variant="ghost" size="sm" onClick={() => onChange("")}>
                <Trash2 className="size-3.5" /> Remove
              </Button>
            )}
          </div>
          <p className="text-xs text-slate-500">JPG or PNG, up to 5 MB. Drag and drop works too.</p>
        </div>
      </div>
      <input
        ref={input}
        type="file"
        accept="image/jpeg,image/png"
        className="hidden"
        onChange={(e) => {
          const f = e.target.files?.[0];
          if (f) upload(f);
        }}
      />
    </div>
  );
}
