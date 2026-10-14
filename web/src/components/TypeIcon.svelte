<script lang="ts">
  import type { Kind } from "../lib/paths";
  import Icon, { type IconName } from "./Icon.svelte";

  let { kind }: { kind: Kind } = $props();
  const icons: Record<Kind, IconName> = {
    folder: "folder",
    image: "image",
    video: "video",
    audio: "audio",
    pdf: "pdf",
    text: "text",
    archive: "archive",
    file: "file",
  };
</script>

<span class="tile kind-{kind}"><Icon name={icons[kind]} size={22} /></span>
