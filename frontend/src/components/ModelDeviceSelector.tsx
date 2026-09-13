import type { ModelHardwareInfo } from "../lib/api";
import * as Icon from "./icons";

function fmtGB(bytes: number): string {
  if (!bytes) return "unknown memory";
  return `${(bytes / 1e9).toFixed(1)} GB free`;
}

export function ModelDeviceSelector({ hardware, disabled = false, onChange }: {
  hardware: ModelHardwareInfo;
  disabled?: boolean;
  onChange: (deviceID: string) => void;
}) {
  const devices = hardware.devices ?? [];
  if (devices.length < 2) return null;

  return (
    <div className="rounded-card border border-line bg-surface p-3.5">
      <div className="flex items-start gap-3">
        <span className="mt-0.5 grid size-7 flex-none place-items-center rounded-full bg-accent-quiet text-accent">
          <Icon.Computer size={14} />
        </span>
        <div className="min-w-0 flex-1">
          <label htmlFor="model-compute-device" className="block text-[13px] font-medium text-text">Model compute device</label>
          <p className="mt-0.5 text-[11.5px] text-faint">Model choices update for the selected GPU's currently available VRAM.</p>
          <div className="relative mt-2.5">
            <select
              id="model-compute-device"
              aria-label="Model compute device"
              value={hardware.selectedDevice || "cpu"}
              disabled={disabled}
              onChange={(event) => onChange(event.target.value)}
              className="h-10 w-full appearance-none rounded-control border border-line-strong bg-bg px-3 pr-9 text-[12.5px] text-text shadow-sm outline-none transition-colors hover:border-accent/60 focus:border-accent focus:ring-2 focus:ring-accent/20 disabled:cursor-wait disabled:opacity-60"
            >
              {devices.map((device) => (
                <option key={device.id} value={device.id}>
                  {device.name || device.id} · {fmtGB(device.freeBytes)}{device.nvidia ? " · NVIDIA" : ""}
                </option>
              ))}
              <option value="cpu">CPU · system memory</option>
            </select>
            <span aria-hidden="true" className="pointer-events-none absolute inset-y-0 right-3 flex items-center text-faint">⌄</span>
          </div>
        </div>
      </div>
    </div>
  );
}
