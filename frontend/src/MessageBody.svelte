<script>
  import { messageMarkdown } from './markdown.js'

  let { text } = $props()
  const content = $derived(messageMarkdown(text))
</script>

{#snippet inline(nodes)}
  {#each nodes as node}{#if node.type === 'strong'}<strong>{@render inline(node.children)}</strong>{:else if node.type === 'em'}<em>{@render inline(node.children)}</em>{:else if node.type === 'del'}<del>{@render inline(node.children)}</del>{:else if node.type === 'inline-code'}<code class="message-inline-code">{@render inline(node.children)}</code>{:else if node.type === 'link'}{#if node.href}<a href={node.href} title={node.title} target="_blank" rel="noopener noreferrer">{@render inline(node.children)}</a>{:else}{@render inline(node.children)}{/if}{:else if node.type === 'br'}<br />{:else}{node.text}{/if}{/each}
{/snippet}

{#snippet blocks(nodes)}
  {#each nodes as block}
    {#if block.type === 'code'}
      <pre class="message-code" aria-label={block.language ? `${block.language} code block` : 'Code block'}><code class={block.language ? `language-${block.language}` : undefined}>{block.text}</code></pre>
    {:else if block.type === 'heading'}
      <svelte:element this={`h${block.level}`} class="message-heading">{@render inline(block.children)}</svelte:element>
    {:else if block.type === 'blockquote'}
      <blockquote>{@render blocks(block.children)}</blockquote>
    {:else if block.type === 'list'}
      {#if block.ordered}<ol start={block.start}>{#each block.items as item}<li>{@render blocks(item)}</li>{/each}</ol>
      {:else}<ul>{#each block.items as item}<li>{@render blocks(item)}</li>{/each}</ul>{/if}
    {:else if block.type === 'table'}
      <div class="message-table-wrap"><table>
        <thead><tr>{#each block.header as cell}<th scope="col" style:text-align={cell.align}>{@render inline(cell.children)}</th>{/each}</tr></thead>
        <tbody>{#each block.rows as row}<tr>{#each row as cell}<td style:text-align={cell.align}>{@render inline(cell.children)}</td>{/each}</tr>{/each}</tbody>
      </table></div>
    {:else if block.type === 'hr'}<hr />
    {:else}<p>{@render inline(block.children)}</p>{/if}
  {/each}
{/snippet}

<div class="message-markdown">{@render blocks(content)}</div>
