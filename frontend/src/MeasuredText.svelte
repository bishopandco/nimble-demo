<script>
  let { text } = $props()

  // Preserve the payload and its whitespace; only insert presentation breaks.
  const segments = $derived.by(() => {
    let words = 0
    return text.split(/(\s+)/u).map((part) => {
      if (/^\s*$/u.test(part)) {
        if (/[\r\n]/u.test(part)) words = 0
        return { text: part, breakBefore: false }
      }
      const breakBefore = words === 10
      words = breakBefore ? 1 : words + 1
      return { text: part, breakBefore }
    })
  })
</script>

{#each segments as segment}{#if segment.breakBefore}<br />{/if}{segment.text}{/each}
