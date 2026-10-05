import { request } from "../http";
import type { ImagesResponse } from "../types";

export const images = () => request<ImagesResponse>("/api/images");

/**
 * Ask the daemon to download and install a published image. It answers as
 * soon as the job is registered — the download is hundreds of MB, so progress
 * comes from polling images(), not from this request.
 */
export const pullImage = (name: string) =>
  request<void>(`/api/images/${encodeURIComponent(name)}/pull`, {
    method: "POST",
  });
