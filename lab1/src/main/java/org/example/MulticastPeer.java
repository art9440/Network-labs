package org.example;

import java.io.Closeable;
import java.io.IOException;
import java.net.*;
import java.nio.charset.StandardCharsets;
import java.util.*;
import java.util.concurrent.*;
import java.util.concurrent.atomic.AtomicBoolean;

public class MulticastPeer implements Closeable {
    private final int port;
    private final InetAddress group;
    private final MulticastSocket socket;

    private final ScheduledExecutorService scheduler = Executors.newScheduledThreadPool(3);

    private final Map<String, PeerInfo> peers = new ConcurrentHashMap<>();

    private final String nodeId = UUID.randomUUID().toString();
    private final AtomicBoolean running = new AtomicBoolean(true);

    private final long periodMs = 1000;
    private final long timeoutMs = 3500;


    public MulticastPeer(int port, InetAddress ipGroup) throws IOException {

        this.port = port; this.group = ipGroup;
        this.socket = new MulticastSocket(port);
        this.socket.setReuseAddress(true);


        this.socket.joinGroup(new InetSocketAddress(group, port), NetworkInterface.getByName("wlo1"));


        startMulticasting();
    }

    private void startMulticasting(){

        scheduler.scheduleAtFixedRate(() -> {
            try {
                String msg = "ALIVE " + nodeId;
                byte[] buf = msg.getBytes(StandardCharsets.UTF_8);
                DatagramPacket p = new DatagramPacket(buf, buf.length, new InetSocketAddress(group, port));
                socket.send(p);
            } catch (Exception ignored) {}
        }, 0, periodMs, TimeUnit.MILLISECONDS);


        scheduler.execute(() -> {
            byte[] buf = new byte[512];
            DatagramPacket p = new DatagramPacket(buf, buf.length);
            while (running.get() && !socket.isClosed()) {
                try {
                    socket.receive(p);
                    String msg = new String(p.getData(), p.getOffset(), p.getLength(), StandardCharsets.UTF_8).trim();

                    if (msg.startsWith("BYE ")) {
                        String peerId = msg.substring(4);
                        if (peers.remove(peerId) != null) {
                            printLive();
                        }
                        continue;
                    }
                    if (!msg.startsWith("ALIVE ")) continue;

                    String peerId = msg.substring(6);
                    long t = System.currentTimeMillis();
                    InetAddress ip = p.getAddress();

                    PeerInfo prev = peers.putIfAbsent(
                            peerId, new PeerInfo(peerId, ip, t, peerId.equals(nodeId))
                    );
                    if (prev == null) {
                        printLive();
                    } else {
                        prev.lastSeen = t;
                    }
                    p.setLength(buf.length);

                } catch (IOException e) {
                    Thread.currentThread().interrupt();
                    System.err.println(e.getMessage());
                }
            }
        });


        scheduler.scheduleAtFixedRate(() -> {
            long t = System.currentTimeMillis();
            boolean changed = peers.entrySet().removeIf(e ->
                    !e.getValue().self && (t - e.getValue().lastSeen > timeoutMs));
            if (changed) printLive();
        }, 500, 500, TimeUnit.MILLISECONDS);
    }


    private void printLive() {
        List<PeerInfo> list = new ArrayList<>(peers.values());
        list.sort(Comparator.<PeerInfo, Boolean>comparing(pi -> !pi.self)
                .thenComparing(pi -> pi.id));

        System.out.println("Alive copies (" + list.size() + "):");
        long now = System.currentTimeMillis();
        for (PeerInfo pi : list) {
            long age = (now - pi.lastSeen);
            String tag = pi.self ? " [self]" : "";
            System.out.printf("  - id=%s ip=%s%s lastSeen=%dms ago%n",
                    pi.id, pi.addr.getHostAddress(), tag, age);
        }
        System.out.println();
    }

    @Override
    public void close(){
        running.set(false);

        try {
            String bye = "BYE " + nodeId;
            byte[] buf = bye.getBytes(StandardCharsets.UTF_8);
            DatagramPacket p = new DatagramPacket(buf, buf.length, new InetSocketAddress(group, port));
            try { socket.send(p); } catch (Exception ignored) {}

        } catch (Exception ignored) {}

        try {
            this.socket.leaveGroup(new InetSocketAddress(group, port), NetworkInterface.getByName("wlo1"));
        } catch (Exception ignored) {}
        socket.close();
        scheduler.shutdownNow();
    }


    private static final class PeerInfo {
        final String id;
        final InetAddress addr;
        volatile long lastSeen;
        final boolean self;

        PeerInfo(String id, InetAddress addr, long lastSeen, boolean self) {
            this.id = id;
            this.addr = addr;
            this.lastSeen = lastSeen;
            this.self = self;
        }
    }
}
