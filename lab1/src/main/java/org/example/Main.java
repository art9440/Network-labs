package org.example;

import java.io.IOException;
import java.net.*;
import java.util.Enumeration;
import java.util.concurrent.CountDownLatch;


public class Main {
    public static void main(String[] args) throws Exception{
        String g = args.length > 0 ? args[0] : "239.255.0.1";
        int port = args.length > 1 ? Integer.parseInt(args[1]) : 50000;

        InetAddress group = InetAddress.getByName(g);
        if (!group.isMulticastAddress()) {
            System.err.println("Not a multicast address: " + g);
            return;
        }


        CountDownLatch stop = new CountDownLatch(1);
        Runtime.getRuntime().addShutdownHook(new Thread(() -> {
            System.out.println("\nShutting down...");
            stop.countDown();
        }));

        try (MulticastPeer peer = new MulticastPeer(port, group)){
            stop.await();
        } catch (IOException e) {
            System.err.println(e.getMessage());
        } catch (InterruptedException e) {
            Thread.currentThread().interrupt();
        }
    }


}