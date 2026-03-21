<script lang="ts">
	import { onMount, onDestroy } from 'svelte';
	import { EditorView, keymap, lineNumbers, highlightActiveLine, highlightActiveLineGutter } from '@codemirror/view';
	import { EditorState } from '@codemirror/state';
	import { json } from '@codemirror/lang-json';
	import { oneDark } from '@codemirror/theme-one-dark';
	import { defaultKeymap, history, historyKeymap } from '@codemirror/commands';
	import { bracketMatching, foldGutter, foldKeymap } from '@codemirror/language';
	import { closeBrackets, closeBracketsKeymap } from '@codemirror/autocomplete';
	import { highlightSelectionMatches, searchKeymap } from '@codemirror/search';
	import { renderPolicy, isParsing, policyText } from '$lib/stores/policy-store';
	import { themeStore } from '$lib/stores';

	interface Props {
		onerror?: (msg: string) => void;
	}

	let { onerror }: Props = $props();

	let editorContainer: HTMLDivElement;
	let view: EditorView | null = null;

	const isDark = $derived($themeStore === 'dark' || ($themeStore === 'system' && typeof window !== 'undefined' && window.matchMedia('(prefers-color-scheme: dark)').matches));

	function handleRender() {
		if (!view) return;
		const text = view.state.doc.toString();
		renderPolicy(text);
	}

	function createExtensions() {
		const exts = [
			lineNumbers(),
			highlightActiveLine(),
			highlightActiveLineGutter(),
			history(),
			bracketMatching(),
			closeBrackets(),
			foldGutter(),
			highlightSelectionMatches(),
			json(),
			keymap.of([
				...defaultKeymap,
				...historyKeymap,
				...foldKeymap,
				...closeBracketsKeymap,
				...searchKeymap,
				{
					key: 'Mod-s',
					run: () => { handleRender(); return true; }
				},
				{
					key: 'Mod-Enter',
					run: () => { handleRender(); return true; }
				}
			]),
			EditorView.theme({
				'&': { height: '100%', fontSize: '13px' },
				'.cm-scroller': { overflow: 'auto', fontFamily: "'SF Mono', Menlo, Consolas, monospace" },
				'.cm-content': { padding: '8px 0' },
				'.cm-gutters': { borderRight: '1px solid var(--color-border)' }
			})
		];

		if (isDark) {
			exts.push(oneDark);
		}

		return exts;
	}

	onMount(() => {
		const state = EditorState.create({
			doc: $policyText || '',
			extensions: createExtensions()
		});

		view = new EditorView({
			state,
			parent: editorContainer
		});
	});

	onDestroy(() => {
		view?.destroy();
	});

	// Sync from store when policy is fetched externally
	let lastStoreText = $policyText;
	$effect(() => {
		const storeText = $policyText;
		if (view && storeText !== lastStoreText && storeText !== view.state.doc.toString()) {
			view.dispatch({
				changes: { from: 0, to: view.state.doc.length, insert: storeText }
			});
			lastStoreText = storeText;
		}
	});
</script>

<div class="flex h-full flex-col">
	<!-- Editor toolbar -->
	<div class="flex items-center justify-between border-b border-border bg-card px-3 py-1.5">
		<span class="text-xs font-medium text-muted-foreground">Policy JSON</span>
		<div class="flex items-center gap-2">
			<span class="text-[10px] text-muted-foreground/50">Cmd+S to render</span>
			<button
				onclick={handleRender}
				disabled={$isParsing}
				class="rounded bg-primary px-2.5 py-1 text-xs font-medium text-primary-foreground hover:bg-primary/90 disabled:opacity-50"
			>
				{$isParsing ? 'Parsing...' : 'Render'}
			</button>
		</div>
	</div>
	<!-- CodeMirror container -->
	<div bind:this={editorContainer} class="flex-1 overflow-hidden"></div>
</div>
