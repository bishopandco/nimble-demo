<script>
  import MeasuredText from './MeasuredText.svelte'
  import { messageBlocks } from './thread.js'

  let { text } = $props()
  const blocks = $derived(messageBlocks(text))
</script>

{#each blocks as block}{#if block.type === 'code'}<pre class="message-code" aria-label={block.language ? `${block.language} code block` : 'Code block'}><code class={block.language ? `language-${block.language}` : undefined}>{block.text}</code></pre>{:else}<MeasuredText text={block.text} />{/if}{/each}
