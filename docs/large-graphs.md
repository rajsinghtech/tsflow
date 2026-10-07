# Large graphs

tsflow still draws a tailnet with ELK's layered layout and Svelte Flow cards. Above 1,200 nodes that pass runs on groups instead of on every device. Virtualization only decides which of those already placed cards are mounted.

## What other tools do

Kiali does not lay out every pod. The graph type is the level of detail: service, app, or workload. Namespace boxes group what is on screen, and a double click opens a detail graph for one node. Edges are already aggregated rates. There is no separate edge bundling pass.

Cilium Hubble UI is a service map for one namespace at a time. Pods are the nodes, recent flows are the edges, and the namespace selector is the level of detail. Filtering, not bundling, is how a busy namespace stays readable.

Weave Scope groups by pod, container, or process and keeps the detail view beside the graph. The canvas is a force layout of the current grouping, not an exploded view of every process in the cluster.

Datadog Service Map and Grafana node graph both draw services, not instances. An edge is the aggregate calls between two services. Datadog's expand is a neighborhood: click a service and the map keeps its callers and callees. Grafana's default layout is layered, in a worker, and it switches to a force layout past a few hundred nodes. Its grid mode drops edges on purpose. That is the look we are not using.

Cytoscape compound nodes, and fCoSE in particular, are the closest library model. A parent owns its children. Incremental layout starts from the positions you already have, animates, and can pin nodes. Edge bundling is a curve style and gets expensive, so large graphs switch to straight edges and hide them while panning.

Sigma.js with Graphology draws 10k nodes as WebGL points. ForceAtlas2 runs in a worker and `animateNodes` eases into the next positions. Labels appear as you zoom. It is the right tool for a dot map, not for the cards this UI already uses.

Gephi's large-graph layouts (ForceAtlas2, OpenOrd, Yifan Hu) are offline or batch. Expand there means a filter or an ego network. Edge bundling is its own statistic, not the interactive layout.

ELK can do this as hierarchy. `SEPARATE_CHILDREN` lays out one compound node's children in their own run, which is an incremental expand. `INCLUDE_CHILDREN` lays out the whole hierarchy at once and will move the other groups. The fixed layout keeps coordinates you already set. Spline routing is a layout option. ELK also has edge bundling, and it is a different algorithm from the curves Svelte Flow draws.

## What tsflow does

Aggregation is the bundling. Devices collapse by tag, then user, then subnet, and the edges between those groups are the summed traffic. The group graph is the same ELK input the homelab graph uses: layered, top to bottom, splines, network simplex, spacing 150. It runs in the existing ELK worker.

Opening a group is one more ELK run over the whole grouped graph, with that group as a compound node (`elk.hierarchyHandling: INCLUDE_CHILDREN`). Members are laid out inside it. Edges that leave the group attach to the compound node, so they meet the group boundary and then the collapsed neighbor. ELK moves the other cards just enough to open a gap. The result is then shifted so the opened group stays centered where it was. Pinning those neighbors at their old coordinates overlaps the cluster, which is why they are not pinned. The camera eases the way a selection already does (300ms on first fit, 600ms when a group opens).

Svelte Flow still draws the cards and the bezier edges, same as a small graph. A group card uses the same frame as a device card. Only cards whose laid-out box meets the viewport are mounted. That cull does not assign positions.

A full graph at or below 300 nodes and 1,500 edges still uses network simplex and spline routing, so those coordinates stay put. Above either limit the layout is the same layered, top-to-bottom pass with the same spacing and crossing sweep, but ELK places nodes with Brandes-Köpf and routes with polylines. Network simplex on a hubby 1,000-node graph was about 11 seconds; the spline routes were never drawn, because Svelte Flow paints its own curves. A refresh that only adds or removes a few nodes keeps the coordinates it already has and sets the newcomers beside a neighbor.

Graphs at or below `FULL_GRAPH_NODE_THRESHOLD` (1,200) never enter the grouped path. The faster placement is what keeps that full layout under a couple of seconds, which is why the cutoff stays here.
