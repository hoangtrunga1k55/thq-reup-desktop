// Maps the backend GET /api/settings response (which prefixes many keys with
// `default_`) to the flat shape the Go engine's Settings struct expects.

import type { BackendSettings } from "./backend";

export function mapBackendSettings(bs: BackendSettings): Record<string, unknown> {
  const g = (k: string) => bs[k];
  return {
    voice: g("default_voice"),
    voice_provider: g("default_voice_provider"),
    speed_adjustment: g("default_speed_adjustment"),

    subtitle_font: g("default_subtitle_font"),
    subtitle_size: g("default_subtitle_size"),
    subtitle_color: g("default_subtitle_color"),
    subtitle_stroke_color: g("default_subtitle_stroke_color"),
    subtitle_stroke_width: g("default_subtitle_stroke_width"),
    subtitle_scale_x: g("default_subtitle_scale_x"),

    cover_opacity: g("default_cover_opacity"),

    flip_video: g("flip_video"),
    video_zoom: g("video_zoom"),
    original_audio_volume: g("original_audio_volume"),

    brand_enabled: g("brand_enabled"),
    brand_type: g("brand_type"),
    brand_text: g("brand_text"),
    brand_text_color: g("brand_text_color"),
    brand_font: g("brand_font"),
    brand_font_size: g("brand_font_size"),
    brand_position: g("brand_position"),
    brand_opacity: g("brand_opacity"),
    brand_image_url: g("brand_image_url"),

    auto_generate_content: g("auto_generate_content"),
    hook_enabled: g("hook_enabled"),
    hook_duration: g("hook_duration"),

    // Pipeline toggles (subtitle off → no burn/cover; voice off → keep original audio)
    enable_subtitle: g("enable_subtitle"),
    enable_voice: g("enable_voice"),

    auto_post_to_facebook: g("auto_post_to_facebook"),
    facebook_page_id: g("facebook_page_id"),
  };
}